package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// The records Cloudflare Email Routing needs for a domain that receives mail. Cloudflare picks the
// MX priorities per zone, so only the hosts are compared.
var (
	routingMXHosts = []string{"route1.mx.cloudflare.net", "route2.mx.cloudflare.net", "route3.mx.cloudflare.net"}
	routingSPF     = "v=spf1 include:_spf.mx.cloudflare.net ~all"
)

// defaultDMARC is added only when a domain has no DMARC record. p=none asks receivers to report,
// not to reject, so it cannot break mail the domain already sends elsewhere.
const defaultDMARC = "v=DMARC1; p=none"

// MailboxDomain is a domain the organization may create addresses on.
type MailboxDomain struct {
	Name string
	// Shared is true for the instance's MAILBOX_DOMAIN, false for a domain the organization connected.
	Shared bool
}

func (s *service) CustomDomainsEnabled() bool {
	return s.settings.CustomDomainsEnabled
}

// MailboxDomains lists the shared domain and the organization's verified domains.
func (s *service) MailboxDomains(ctx context.Context, organizationID uint) ([]MailboxDomain, error) {
	domains := []MailboxDomain{{Name: s.settings.Domain, Shared: true}}
	if !s.settings.CustomDomainsEnabled || s.domains == nil {
		return domains, nil
	}
	connected, err := s.domains.ListDomains(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	for _, mailDomain := range connected {
		if mailDomain.Status == domain.MailDomainStatusVerified {
			domains = append(domains, MailboxDomain{Name: mailDomain.Name})
		}
	}
	return domains, nil
}

func (s *service) requireDomains() error {
	if !s.settings.CustomDomainsEnabled || s.domains == nil || s.domainProvider == nil {
		return ErrMailDomainsDisabled
	}
	return nil
}

func (s *service) ListMailDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	return s.domains.ListDomains(ctx, organizationID)
}

func (s *service) GetMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	return s.domains.FindDomain(ctx, organizationID, id)
}

// normalizeDomainName lowercases a typed domain and rejects what no mailbox may use, including
// the shared domain, which belongs to every organization.
func (s *service) normalizeDomainName(name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if !config.ValidMailboxDomain(name) || strings.HasSuffix(name, ".localhost") || strings.Count(name, ".") < 1 {
		return "", ErrMailDomainInvalid
	}
	if name == s.settings.Domain {
		return "", ErrMailDomainTaken
	}
	return name, nil
}

// ConnectMailDomain claims a domain for the organization and runs its setup. A provider failure
// during setup does not fail the call: the domain stays pending with the reason, and the owner
// can run the setup again.
func (s *service) ConnectMailDomain(ctx context.Context, organizationID, createdBy uint, name string) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	name, err := s.normalizeDomainName(name)
	if err != nil {
		return nil, err
	}
	if _, lookupErr := s.domains.FindDomainByName(ctx, name); lookupErr == nil {
		return nil, ErrMailDomainTaken
	} else if !errors.Is(lookupErr, ErrMailDomainNotFound) {
		return nil, lookupErr
	}
	zone, err := s.domainProvider.FindZone(ctx, name)
	if err != nil {
		return nil, providerError(err)
	}
	mailDomain := &domain.MailDomain{
		OrganizationID: organizationID,
		Name:           name,
		ZoneID:         zone.ID,
		ZoneName:       zone.Name,
		Status:         domain.MailDomainStatusPending,
		CreatedBy:      createdBy,
	}
	if err := s.domains.CreateDomain(ctx, mailDomain, domain.MaxMailDomainsPerOrganization); err != nil {
		return nil, err
	}
	return s.setUp(ctx, mailDomain)
}

// providerError maps a provider failure on a step that cannot continue without it.
func providerError(err error) error {
	switch {
	case errors.Is(err, mailtransport.ErrZoneNotFound):
		return ErrMailDomainZoneNotFound
	case errors.Is(err, mailtransport.ErrNotConfigured):
		return ErrMailDomainsDisabled
	}
	var failure *mailtransport.DomainError
	if errors.As(err, &failure) {
		return fmt.Errorf("%w: %s", ErrMailDomainProviderFailed, failure.Error())
	}
	return err
}

// SetUpMailDomain runs the setup again. Every step is safe to repeat.
func (s *service) SetUpMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	mailDomain, err := s.domains.FindDomain(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	return s.setUp(ctx, mailDomain)
}

// CheckMailDomain compares the zone's records with what the domain needs, without writing DNS.
func (s *service) CheckMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	mailDomain, err := s.domains.FindDomain(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	return s.check(ctx, mailDomain, nil)
}

func (s *service) DeleteMailDomain(ctx context.Context, organizationID, id uint) error {
	if err := s.requireDomains(); err != nil {
		return err
	}
	return s.domains.DeleteDomain(ctx, organizationID, id)
}

// setUp enables receiving and sending, adds the sending and DMARC records the zone lacks, and
// checks the result. It only ever adds: a record that conflicts stays as it is and is reported.
func (s *service) setUp(ctx context.Context, mailDomain *domain.MailDomain) (*domain.MailDomain, error) {
	var failures []error
	if err := s.domainProvider.EnableRouting(ctx, mailDomain.ZoneID, mailDomain.Name); err != nil {
		failures = append(failures, err)
	}
	if tag, err := s.domainProvider.EnsureSending(ctx, mailDomain.ZoneID, mailDomain.Name); err != nil {
		failures = append(failures, err)
	} else {
		mailDomain.SendingTag = tag
	}
	records, err := s.evaluate(ctx, mailDomain)
	if err != nil {
		failures = append(failures, err)
	} else {
		for _, record := range records {
			if record.Status != domain.MailDomainRecordMissing || record.Purpose == domain.MailDomainRecordReceiving ||
				record.Content == "" {
				// Email Routing owns the receiving records; Zmail never writes them itself. A record
				// without content stands for sending that is not enabled yet.
				continue
			}
			if err := s.domainProvider.CreateRecord(ctx, mailDomain.ZoneID, mailtransport.DNSRecord{
				Type: record.Type, Name: record.Name, Content: record.Content, Priority: record.Priority,
			}); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return s.check(ctx, mailDomain, failures)
}

// check evaluates the domain, stores the result, and keeps the first failure as the reason when
// the domain is still not verified.
func (s *service) check(ctx context.Context, mailDomain *domain.MailDomain, failures []error) (*domain.MailDomain, error) {
	now := s.now().UTC()
	records, err := s.evaluate(ctx, mailDomain)
	if err != nil {
		failures = append(failures, err)
	} else {
		mailDomain.Records = records
	}
	mailDomain.CheckedAt = &now
	verified := err == nil && len(records) > 0
	for _, record := range records {
		if record.Status != domain.MailDomainRecordOK {
			verified = false
		}
	}
	if verified {
		mailDomain.Status = domain.MailDomainStatusVerified
		if mailDomain.VerifiedAt == nil {
			mailDomain.VerifiedAt = &now
		}
		mailDomain.LastError = ""
	} else {
		mailDomain.Status = domain.MailDomainStatusPending
		mailDomain.LastError = ""
		if len(failures) > 0 {
			mailDomain.LastError = failures[0].Error()
			slog.WarnContext(ctx, "mail domain setup or check failed",
				slog.String("domain", mailDomain.Name), slog.String("error", mailDomain.LastError))
		}
	}
	if err := s.domains.UpdateDomain(ctx, mailDomain); err != nil {
		return nil, err
	}
	return mailDomain, nil
}

// evaluate lists the records the domain needs and what its zone has for each.
func (s *service) evaluate(ctx context.Context, mailDomain *domain.MailDomain) ([]domain.MailDomainRecord, error) {
	required := make([]domain.MailDomainRecord, 0, 8)
	for _, host := range routingMXHosts {
		required = append(required, domain.MailDomainRecord{
			Purpose: domain.MailDomainRecordReceiving, Type: "MX", Name: mailDomain.Name, Content: host,
		})
	}
	required = append(required, domain.MailDomainRecord{
		Purpose: domain.MailDomainRecordReceiving, Type: "TXT", Name: mailDomain.Name, Content: routingSPF,
	})
	if mailDomain.SendingTag != "" {
		sending, err := s.domainProvider.SendingRecords(ctx, mailDomain.ZoneID, mailDomain.SendingTag)
		if err != nil {
			return nil, err
		}
		for _, record := range sending {
			required = append(required, domain.MailDomainRecord{
				Purpose: domain.MailDomainRecordSending, Type: record.Type, Name: record.Name,
				Content: record.Content, Priority: record.Priority,
			})
		}
	} else {
		// Sending was never enabled; report it as missing so the domain cannot verify.
		required = append(required, domain.MailDomainRecord{
			Purpose: domain.MailDomainRecordSending, Type: "TXT", Name: mailDomain.Name, Content: "",
		})
	}
	required = append(required, domain.MailDomainRecord{
		Purpose: domain.MailDomainRecordDMARC, Type: "TXT", Name: "_dmarc." + mailDomain.Name, Content: defaultDMARC,
	})

	existing := map[string][]mailtransport.DNSRecord{}
	for i := range required {
		name := strings.ToLower(required[i].Name)
		if _, ok := existing[name]; !ok {
			records, err := s.domainProvider.ListRecords(ctx, mailDomain.ZoneID, name)
			if err != nil {
				return nil, err
			}
			existing[name] = records
		}
		required[i] = compareRecord(required[i], existing[name])
	}
	return required, nil
}

// compareRecord fills Status and, for a conflict, Found. TXT records of the same kind (SPF,
// DMARC, DKIM) share a name with unrelated TXT records, so they are matched by their version tag.
func compareRecord(required domain.MailDomainRecord, existing []mailtransport.DNSRecord) domain.MailDomainRecord {
	required.Status = domain.MailDomainRecordMissing
	if required.Content == "" {
		return required
	}
	want := normalizeRecordContent(required.Type, required.Content)
	tag := recordTag(want)
	for _, record := range existing {
		if !strings.EqualFold(record.Type, required.Type) {
			continue
		}
		have := normalizeRecordContent(record.Type, record.Content)
		switch {
		case have == want:
			required.Status, required.Found = domain.MailDomainRecordOK, ""
			return required
		case required.Purpose == domain.MailDomainRecordDMARC && recordTag(have) == "v=dmarc1":
			// Any DMARC policy satisfies the check; Zmail only adds one when there is none.
			required.Status, required.Content, required.Found = domain.MailDomainRecordOK, record.Content, ""
			return required
		case tag == "v=spf1" && recordTag(have) == "v=spf1" && spfIncludes(have, want):
			required.Status, required.Found = domain.MailDomainRecordOK, ""
			return required
		case required.Type == "TXT" && tag != "" && recordTag(have) == tag,
			required.Type == "CNAME",
			required.Type == "MX" && !strings.HasSuffix(have, ".mx.cloudflare.net"):
			required.Status, required.Found = domain.MailDomainRecordConflict, record.Content
		}
	}
	return required
}

// normalizeRecordContent removes quoting, chunking, trailing dots, and case where DNS ignores
// them, so a record compares equal however the provider prints it.
func normalizeRecordContent(recordType, content string) string {
	content = strings.TrimSpace(content)
	if strings.EqualFold(recordType, "TXT") {
		if strings.HasPrefix(content, `"`) && strings.HasSuffix(content, `"`) {
			content = strings.ReplaceAll(content[1:len(content)-1], `" "`, "")
		}
		return strings.Join(strings.Fields(content), " ")
	}
	return strings.TrimSuffix(strings.ToLower(content), ".")
}

// recordTag returns the lowercase version tag of a TXT record, such as "v=spf1", or "".
func recordTag(content string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(content), ";")
	first, _, _ = strings.Cut(first, " ")
	first = strings.ToLower(strings.TrimSpace(first))
	if strings.HasPrefix(first, "v=") {
		return first
	}
	return ""
}

// spfIncludes reports whether the existing SPF record has every include of the required one.
func spfIncludes(have, want string) bool {
	present := map[string]bool{}
	for _, term := range strings.Fields(strings.ToLower(have)) {
		present[term] = true
	}
	for _, term := range strings.Fields(strings.ToLower(want)) {
		if strings.HasPrefix(term, "include:") && !present[term] {
			return false
		}
	}
	return true
}

// mailboxDomainFor resolves where a new address goes: the shared domain, or a verified domain of
// the organization. It returns the connected domain, or nil for the shared one.
func (s *service) mailboxDomainFor(ctx context.Context, organizationID uint, name string) (string, *domain.MailDomain, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || name == s.settings.Domain {
		return s.settings.Domain, nil, nil
	}
	if err := s.requireDomains(); err != nil {
		return "", nil, ErrMailDomainNotFound
	}
	mailDomain, err := s.domains.FindDomainByName(ctx, name)
	if err != nil {
		return "", nil, err
	}
	if mailDomain.OrganizationID != organizationID {
		return "", nil, ErrMailDomainNotFound
	}
	if mailDomain.Status != domain.MailDomainStatusVerified {
		return "", nil, ErrMailDomainNotVerified
	}
	return mailDomain.Name, mailDomain, nil
}

// removeRoutingRule deletes the rule of a deleted mailbox. A failure only leaves a rule that hands
// mail to the Worker for an address the API no longer accepts, so it is logged, not returned.
func (s *service) removeRoutingRule(ctx context.Context, mailbox *domain.Mailbox) {
	if mailbox.RoutingRuleID == "" || s.domains == nil || s.domainProvider == nil {
		return
	}
	_, domainName, _ := strings.Cut(mailbox.Address, "@")
	mailDomain, err := s.domains.FindDomainByName(ctx, domainName)
	if err == nil {
		err = s.domainProvider.DeleteRoutingRule(ctx, mailDomain.ZoneID, mailbox.RoutingRuleID)
	}
	if err != nil {
		slog.WarnContext(ctx, "routing rule of a deleted mailbox was not removed",
			slog.String("address", mailbox.Address), slog.String("error", err.Error()))
	}
}
