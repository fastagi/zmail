package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

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

// domainSetupTimeout bounds one background setup: a handful of provider calls.
const domainSetupTimeout = 2 * time.Minute

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

func (s *service) domainsEnabled() bool {
	return s.settings.CustomDomainsEnabled && s.domains != nil && s.zones != nil && s.domainProvider != nil
}

func (s *service) requireDomains() error {
	if !s.domainsEnabled() {
		return ErrMailDomainsDisabled
	}
	return nil
}

func (s *service) ListMailDomains(ctx context.Context, organizationID uint) ([]*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	domains, err := s.domains.ListDomains(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if err := s.attachZones(ctx, organizationID, domains); err != nil {
		return nil, err
	}
	for _, mailDomain := range domains {
		s.markSettingUp(mailDomain)
	}
	return domains, nil
}

func (s *service) GetMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	mailDomain, err := s.domains.FindDomain(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	return s.present(ctx, mailDomain)
}

// present fills what a domain returned to a caller carries besides its stored fields.
func (s *service) present(ctx context.Context, mailDomain *domain.MailDomain) (*domain.MailDomain, error) {
	if err := s.attachZones(ctx, mailDomain.OrganizationID, []*domain.MailDomain{mailDomain}); err != nil {
		return nil, err
	}
	return s.markSettingUp(mailDomain), nil
}

func (s *service) markSettingUp(mailDomain *domain.MailDomain) *domain.MailDomain {
	mailDomain.SettingUp = s.setupRunning(mailDomain.ID)
	return mailDomain
}

// setupRunning reports whether a setup of the domain runs in the background.
func (s *service) setupRunning(id uint) bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	_, running := s.domainSetups[id]
	return running
}

// domainSetupRun is one background setup of a domain. rerun is set when the domain changed, such
// as its capability, while the run was in progress, so the run sets it up again before ending.
// Both fields of the run and its place in domainSetups change only under setupMu.
type domainSetupRun struct {
	rerun bool
}

// startSetUp runs the setup of a domain in the background, once at a time per domain, and returns
// the domain as it is now. The setup makes several provider calls, which can outlast the request
// and the proxies in front of it; a background run is never cut off by a client that stops waiting.
// A call while a setup runs asks that run to set the domain up once more, from the domain as stored
// then, so a change made meanwhile is never lost to a run that read the domain before it. Asking
// and ending happen under one lock, so a run either sees the request or has already ended and a
// new run starts.
func (s *service) startSetUp(ctx context.Context, mailDomain *domain.MailDomain) (*domain.MailDomain, error) {
	s.setupMu.Lock()
	run, running := s.domainSetups[mailDomain.ID]
	if running {
		run.rerun = true
	} else {
		if s.domainSetups == nil {
			s.domainSetups = map[uint]*domainSetupRun{}
		}
		run = &domainSetupRun{}
		s.domainSetups[mailDomain.ID] = run
	}
	s.setupMu.Unlock()
	if running {
		if s.setupClaimHook != nil {
			s.setupClaimHook()
		}
	} else {
		target := *mailDomain
		s.setups.Add(1)
		s.background(func() {
			defer s.setups.Done()
			s.runSetUps(ctx, run, &target)
		})
	}
	current, err := s.domains.FindDomain(ctx, mailDomain.OrganizationID, mailDomain.ID)
	if err != nil {
		return nil, err
	}
	return s.present(ctx, current)
}

// runSetUps sets the domain up until no rerun is asked for, reading the stored domain again for
// every run after the first, and then releases the domain.
func (s *service) runSetUps(ctx context.Context, run *domainSetupRun, target *domain.MailDomain) {
	for {
		s.runSetUp(ctx, target)
		s.setupMu.Lock()
		if !run.rerun {
			delete(s.domainSetups, target.ID)
			s.setupMu.Unlock()
			return
		}
		run.rerun = false
		s.setupMu.Unlock()
		if !s.reloadSetUpTarget(ctx, target) {
			// The domain is gone; nothing is left to set up, whatever was asked meanwhile.
			s.setupMu.Lock()
			delete(s.domainSetups, target.ID)
			s.setupMu.Unlock()
			return
		}
	}
}

func (s *service) runSetUp(ctx context.Context, target *domain.MailDomain) {
	setupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), domainSetupTimeout)
	defer cancel()
	if _, err := s.setUp(setupContext, target); err != nil {
		slog.WarnContext(setupContext, "mail domain setup could not be stored",
			slog.String("domain", target.Name), slog.String("error", err.Error()))
	}
}

// reloadSetUpTarget replaces target with the stored domain and reports whether it still exists.
func (s *service) reloadSetUpTarget(ctx context.Context, target *domain.MailDomain) bool {
	current, err := s.domains.FindDomain(context.WithoutCancel(ctx), target.OrganizationID, target.ID)
	if err != nil {
		if !errors.Is(err, ErrMailDomainNotFound) {
			slog.WarnContext(ctx, "mail domain setup was not repeated",
				slog.String("domain", target.Name), slog.String("error", err.Error()))
		}
		return false
	}
	*target = *current
	return true
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

// ConnectMailDomain claims a domain for the organization in the zone that holds it, creating a
// managed zone when the provider account has none, and runs its setup once the zone is in use. A
// provider failure during setup does not fail the call: the domain stays pending with the
// reason, and the owner can run the setup again. An empty capability means send_receive.
func (s *service) ConnectMailDomain(ctx context.Context, organizationID, createdBy uint, name string, capability domain.MailDomainCapability) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	name, err := s.normalizeDomainName(name)
	if err != nil {
		return nil, err
	}
	switch capability {
	case "":
		capability = domain.MailDomainSendReceive
	case domain.MailDomainSendReceive, domain.MailDomainSendOnly:
	default:
		return nil, ErrMailDomainInvalid
	}
	if _, lookupErr := s.domains.FindDomainByName(ctx, name); lookupErr == nil {
		return nil, ErrMailDomainTaken
	} else if !errors.Is(lookupErr, ErrMailDomainNotFound) {
		return nil, lookupErr
	}
	zone, err := s.resolveZone(ctx, organizationID, createdBy, name)
	if err != nil {
		return nil, err
	}
	mailDomain := &domain.MailDomain{
		OrganizationID: organizationID,
		Name:           name,
		ZoneID:         zone.providerID,
		ZoneName:       zone.name,
		Capability:     capability,
		Status:         domain.MailDomainStatusPending,
		CreatedBy:      createdBy,
	}
	if zone.managed != nil {
		zoneID := zone.managed.ID
		mailDomain.MailZoneID = &zoneID
	}
	if err := s.domains.CreateDomain(ctx, mailDomain, domain.MaxMailDomainsPerOrganization); err != nil {
		if zone.created {
			s.discardManagedZone(ctx, zone.managed)
		}
		return nil, err
	}
	if zone.managed != nil && zone.managed.Status != domain.MailZoneStatusActive {
		// The provider does not serve the zone until the registrar delegates it, so the setup
		// waits for the zone to become active.
		mailDomain.Zone = zone.managed
		return s.markSettingUp(mailDomain), nil
	}
	return s.startSetUp(ctx, mailDomain)
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
	pending, err := s.pendingZoneOf(ctx, mailDomain)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		// The provider does not serve the zone until the registrar delegates it.
		return nil, ErrMailZonePending
	}
	return s.startSetUp(ctx, mailDomain)
}

// CheckMailDomain compares the zone's records with what the domain needs, without writing DNS.
// While the domain's managed zone is pending, it checks the zone's delegation instead.
func (s *service) CheckMailDomain(ctx context.Context, organizationID, id uint) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	mailDomain, err := s.domains.FindDomain(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	pending, err := s.pendingZoneOf(ctx, mailDomain)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		return s.checkDomainZone(ctx, mailDomain, pending)
	}
	if s.setupRunning(id) {
		// The running setup checks when it is done; a second check now would race it.
		return s.present(ctx, mailDomain)
	}
	checked, err := s.check(ctx, mailDomain, nil)
	if err != nil {
		return nil, err
	}
	return s.present(ctx, checked)
}

func (s *service) DeleteMailDomain(ctx context.Context, organizationID, id uint) error {
	if err := s.requireDomains(); err != nil {
		return err
	}
	return s.domains.DeleteDomain(ctx, organizationID, id)
}

// setUp enables receiving (unless the domain only sends) and sending, adds the sending and DMARC
// records the zone lacks, and checks the result. It only ever adds: a record that conflicts stays
// as it is and is reported.
func (s *service) setUp(ctx context.Context, mailDomain *domain.MailDomain) (*domain.MailDomain, error) {
	var failures []error
	if receives(mailDomain) {
		if err := s.domainProvider.EnableRouting(ctx, mailDomain.ZoneID, mailDomain.Name); err != nil {
			failures = append(failures, err)
		} else {
			failures = append(failures, s.routeUnroutedMailboxes(ctx, mailDomain)...)
		}
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
			if _, err := s.domainProvider.CreateRecord(ctx, mailDomain.ZoneID, mailtransport.DNSRecord{
				Type: record.Type, Name: record.Name, Content: record.Content, Priority: record.Priority,
			}); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return s.check(ctx, mailDomain, failures)
}

// routeUnroutedMailboxes gives every live mailbox on a receiving domain that has no routing rule
// its rule, the way CreateMailbox does. Such mailboxes were created while the domain only sent.
// Mailboxes that already have a rule are left alone, so a repeated setup adds nothing.
func (s *service) routeUnroutedMailboxes(ctx context.Context, mailDomain *domain.MailDomain) []error {
	mailboxes, err := s.domains.ListMailboxesWithoutRoutingRule(ctx, mailDomain.OrganizationID, mailDomain.Name)
	if err != nil {
		return []error{err}
	}
	var failures []error
	for _, mailbox := range mailboxes {
		ruleID, err := s.domainProvider.CreateRoutingRule(ctx, mailDomain.ZoneID, mailbox.Address, s.settings.EmailWorker)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := s.domains.SetMailboxRoutingRule(ctx, mailbox.ID, ruleID); err != nil {
			failures = append(failures, err)
			// An unrecorded rule would never be removed with its mailbox.
			if deleteErr := s.domainProvider.DeleteRoutingRule(ctx, mailDomain.ZoneID, ruleID); deleteErr != nil {
				slog.WarnContext(ctx, "unrecorded routing rule was not removed",
					slog.String("address", mailbox.Address), slog.String("error", deleteErr.Error()))
			}
		}
	}
	for _, failure := range failures {
		slog.WarnContext(ctx, "existing mailbox did not get a routing rule",
			slog.String("domain", mailDomain.Name), slog.String("error", failure.Error()))
	}
	return failures
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

// receives reports whether the domain receives mail through Zmail. A send-only domain leaves its
// MX with another mail service, so Email Routing is never enabled for it.
func receives(mailDomain *domain.MailDomain) bool {
	return mailDomain.Capability != domain.MailDomainSendOnly
}

// evaluate lists the records the domain needs and what its zone has for each.
func (s *service) evaluate(ctx context.Context, mailDomain *domain.MailDomain) ([]domain.MailDomainRecord, error) {
	required := make([]domain.MailDomainRecord, 0, 8)
	if receives(mailDomain) {
		for _, host := range routingMXHosts {
			required = append(required, domain.MailDomainRecord{
				Purpose: domain.MailDomainRecordReceiving, Type: "MX", Name: mailDomain.Name, Content: host,
			})
		}
		required = append(required, domain.MailDomainRecord{
			Purpose: domain.MailDomainRecordReceiving, Type: "TXT", Name: mailDomain.Name, Content: routingSPF,
		})
	}
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
	// Cloudflare lists a DMARC record among the sending records; that one stands for DMARC, so the
	// domain does not show two rows for the same record.
	dmarcName := "_dmarc." + mailDomain.Name
	hasDMARC := false
	for i := range required {
		if required[i].Type == "TXT" && strings.EqualFold(required[i].Name, dmarcName) {
			required[i].Purpose = domain.MailDomainRecordDMARC
			hasDMARC = true
		}
	}
	if !hasDMARC {
		required = append(required, domain.MailDomainRecord{
			Purpose: domain.MailDomainRecordDMARC, Type: "TXT", Name: dmarcName, Content: defaultDMARC,
		})
	}

	// Each name is read once, all names at the same time.
	names := []string{}
	seen := map[string]bool{}
	for _, record := range required {
		name := strings.ToLower(record.Name)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	found := make([][]mailtransport.DNSRecord, len(names))
	group, groupContext := errgroup.WithContext(ctx)
	for i, name := range names {
		group.Go(func() error {
			records, err := s.domainProvider.ListRecords(groupContext, mailDomain.ZoneID, name)
			found[i] = records
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	existing := make(map[string][]mailtransport.DNSRecord, len(names))
	for i, name := range names {
		existing[name] = found[i]
	}
	for i := range required {
		required[i] = compareRecord(required[i], existing[strings.ToLower(required[i].Name)])
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
		if !receives(mailDomain) {
			// Email Routing is never used for a send-only domain.
			return
		}
		err = s.domainProvider.DeleteRoutingRule(ctx, mailDomain.ZoneID, mailbox.RoutingRuleID)
	}
	if err != nil {
		slog.WarnContext(ctx, "routing rule of a deleted mailbox was not removed",
			slog.String("address", mailbox.Address), slog.String("error", err.Error()))
	}
}

// WaitForSetups waits until no domain setup runs in the background, or until ctx ends. A worker
// calls it before it exits, so an activation's setups are not cut off with the process.
func (s *service) WaitForSetups(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.setups.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
