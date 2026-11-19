package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// Limits of the DNS record editor.
const (
	// maxDNSRecordsPerZone is Cloudflare's record quota for one zone, which bounds a list.
	maxDNSRecordsPerZone = 3500
	maxTXTContentLength  = 2048
	maxDNSNameLength     = 253
	maxDNSLabelLength    = 63
	// automaticTTL lets Cloudflare choose the TTL.
	automaticTTL  = 1
	minDNSTTL     = 60
	maxDNSTTL     = 86400
	maxMXPriority = 65535
)

// Why a record is locked: Zmail mail relies on it.
const (
	dnsLockReceiving = "mail_receiving"
	dnsLockSPF       = "mail_spf"
	dnsLockDMARC     = "mail_dmarc"
	dnsLockDKIM      = "mail_dkim"
	dnsLockSending   = "mail_sending"
)

// editableDNSTypes are the record types the editor writes.
var editableDNSTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "TXT": true, "MX": true}

// proxiableDNSTypes are the record types Cloudflare may proxy.
var proxiableDNSTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true}

// MailDNSRecord is one record of a managed zone, with why Zmail mail relies on it, if it does.
type MailDNSRecord struct {
	mailtransport.DNSRecord
	// LockedReason is empty when the record may be changed.
	LockedReason string
}

// MailDNSRecordInput is a new record. Name may be "@" or a single label relative to the zone.
// A nil TTL is automatic; Priority is required for MX and ignored otherwise.
type MailDNSRecordInput struct {
	Type     string
	Name     string
	Content  string
	TTL      *int
	Priority *int
	Proxied  bool
}

// MailDNSRecordPatch changes a record; a nil field keeps its current value.
type MailDNSRecordPatch struct {
	Type     *string
	Name     *string
	Content  *string
	TTL      *int
	Priority *int
	Proxied  *bool
}

// DNSRecordInvalidError names the field of a record that failed validation.
type DNSRecordInvalidError struct {
	Field  string
	Reason string
}

func (e *DNSRecordInvalidError) Error() string {
	return fmt.Sprintf("%s: %s %s", ErrMailDNSRecordInvalid, e.Field, e.Reason)
}

func (e *DNSRecordInvalidError) Unwrap() error { return ErrMailDNSRecordInvalid }

func invalidRecord(field, reason string) error {
	return &DNSRecordInvalidError{Field: field, Reason: reason}
}

// lockReason tells why Zmail mail relies on a record, or "" when it does not: for each mail domain
// d of the zone, the MX at d, an SPF TXT at d, anything at _dmarc.d or under _domainkey.d, and the
// sending records stored on d.
func lockReason(record mailtransport.DNSRecord, domains []*domain.MailDomain) string {
	recordType := strings.ToUpper(record.Type)
	name := normalizeDNSName(record.Name)
	for _, mailDomain := range domains {
		domainName := normalizeDNSName(mailDomain.Name)
		switch {
		case recordType == "MX" && name == domainName:
			return dnsLockReceiving
		case recordType == "TXT" && name == domainName &&
			recordTag(normalizeRecordContent("TXT", record.Content)) == "v=spf1":
			return dnsLockSPF
		case name == "_dmarc."+domainName:
			return dnsLockDMARC
		case strings.HasSuffix(name, "._domainkey."+domainName):
			return dnsLockDKIM
		}
		for _, required := range mailDomain.Records {
			if required.Purpose == domain.MailDomainRecordSending && strings.EqualFold(required.Type, recordType) &&
				normalizeDNSName(required.Name) == name &&
				normalizeRecordContent(recordType, required.Content) == normalizeRecordContent(recordType, record.Content) {
				return dnsLockSending
			}
		}
	}
	return ""
}

func normalizeDNSName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// dnsZone is a managed zone with the records and mail domains it holds now.
type dnsZone struct {
	zone    *domain.MailZone
	records []mailtransport.DNSRecord
	domains []*domain.MailDomain
}

// loadDNSZone reads an organization's managed zone, its records from Cloudflare, and its mail
// domains. A zone of another organization is ErrMailZoneNotFound.
func (s *service) loadDNSZone(ctx context.Context, organizationID, zoneID uint) (*dnsZone, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	zone, err := s.zones.FindZone(ctx, organizationID, zoneID)
	if err != nil {
		return nil, err
	}
	records, err := s.domainProvider.ListZoneRecords(ctx, zone.CloudflareZoneID)
	if err != nil {
		return nil, providerError(err)
	}
	domains, err := s.zones.ListDomainsInZone(ctx, zone.ID)
	if err != nil {
		return nil, err
	}
	return &dnsZone{zone: zone, records: records, domains: domains}, nil
}

func (z *dnsZone) find(recordID string) (mailtransport.DNSRecord, error) {
	for _, record := range z.records {
		if record.ID == recordID {
			return record, nil
		}
	}
	return mailtransport.DNSRecord{}, ErrMailDNSRecordNotFound
}

// ListMailDNSRecords lists the records of the zone, by name and type, marking those Zmail mail
// relies on. Cloudflare is the only copy; nothing is stored. At most maxDNSRecordsPerZone are
// returned.
func (s *service) ListMailDNSRecords(ctx context.Context, organizationID, zoneID uint) ([]MailDNSRecord, error) {
	zone, err := s.loadDNSZone(ctx, organizationID, zoneID)
	if err != nil {
		return nil, err
	}
	records := make([]MailDNSRecord, 0, len(zone.records))
	for _, record := range zone.records {
		records = append(records, MailDNSRecord{DNSRecord: record, LockedReason: lockReason(record, zone.domains)})
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Name != records[j].Name {
			return records[i].Name < records[j].Name
		}
		return records[i].Type < records[j].Type
	})
	if len(records) > maxDNSRecordsPerZone {
		// Only a zone on a plan with a higher quota gets here; the list stays bounded.
		slog.WarnContext(ctx, "DNS record list truncated",
			slog.String("zone", zone.zone.Name), slog.Int("records", len(records)))
		records = records[:maxDNSRecordsPerZone]
	}
	return records, nil
}

// CreateMailDNSRecord validates a new record and writes it to Cloudflare, unless Zmail mail
// relies on a record like it.
func (s *service) CreateMailDNSRecord(ctx context.Context, organizationID, zoneID uint, input MailDNSRecordInput) (*MailDNSRecord, error) {
	zone, err := s.loadDNSZone(ctx, organizationID, zoneID)
	if err != nil {
		return nil, err
	}
	ttl := automaticTTL
	if input.TTL != nil {
		ttl = *input.TTL
	}
	record := mailtransport.DNSRecord{
		Type: strings.ToUpper(strings.TrimSpace(input.Type)), Name: input.Name,
		Content: strings.TrimSpace(input.Content), TTL: ttl, Proxied: input.Proxied,
	}
	if record, err = zone.validate(record, input.Priority); err != nil {
		return nil, err
	}
	if lockReason(record, zone.domains) != "" {
		return nil, ErrMailDNSRecordLocked
	}
	created, err := s.domainProvider.CreateRecord(ctx, zone.zone.CloudflareZoneID, record)
	if err != nil {
		return nil, recordWriteError(err)
	}
	s.auditDNSRecord(ctx, "create", zone.zone, created.ID, nil, &created)
	return &MailDNSRecord{DNSRecord: created}, nil
}

// UpdateMailDNSRecord applies patch onto the record as Cloudflare has it now, validates the
// result, and rewrites the whole record. Neither the record nor its result may be one Zmail mail
// relies on.
func (s *service) UpdateMailDNSRecord(ctx context.Context, organizationID, zoneID uint, recordID string, patch MailDNSRecordPatch) (*MailDNSRecord, error) {
	zone, err := s.loadDNSZone(ctx, organizationID, zoneID)
	if err != nil {
		return nil, err
	}
	current, err := zone.find(recordID)
	if err != nil {
		return nil, err
	}
	if lockReason(current, zone.domains) != "" {
		return nil, ErrMailDNSRecordLocked
	}
	target := current
	var priority *int
	if strings.EqualFold(current.Type, "MX") {
		priority = &current.Priority
	}
	if patch.Type != nil {
		target.Type = strings.ToUpper(strings.TrimSpace(*patch.Type))
	}
	if patch.Name != nil {
		target.Name = *patch.Name
	}
	if patch.Content != nil {
		target.Content = strings.TrimSpace(*patch.Content)
	}
	if patch.TTL != nil {
		target.TTL = *patch.TTL
	}
	if patch.Priority != nil {
		priority = patch.Priority
	}
	if patch.Proxied != nil {
		target.Proxied = *patch.Proxied
	}
	if target, err = zone.validate(target, priority); err != nil {
		return nil, err
	}
	if lockReason(target, zone.domains) != "" {
		return nil, ErrMailDNSRecordLocked
	}
	if err := s.domainProvider.UpdateRecord(ctx, zone.zone.CloudflareZoneID, target); err != nil {
		return nil, recordWriteError(err)
	}
	s.auditDNSRecord(ctx, "update", zone.zone, target.ID, &current, &target)
	return &MailDNSRecord{DNSRecord: target}, nil
}

// DeleteMailDNSRecord removes a record of a type the editor writes that Zmail mail does not rely
// on.
func (s *service) DeleteMailDNSRecord(ctx context.Context, organizationID, zoneID uint, recordID string) error {
	zone, err := s.loadDNSZone(ctx, organizationID, zoneID)
	if err != nil {
		return err
	}
	current, err := zone.find(recordID)
	if err != nil {
		return err
	}
	if !editableDNSTypes[strings.ToUpper(current.Type)] {
		return invalidRecord("type", "only A, AAAA, CNAME, TXT, and MX records can be deleted")
	}
	if lockReason(current, zone.domains) != "" {
		return ErrMailDNSRecordLocked
	}
	if err := s.domainProvider.DeleteRecord(ctx, zone.zone.CloudflareZoneID, current.ID); err != nil {
		return recordWriteError(err)
	}
	s.auditDNSRecord(ctx, "delete", zone.zone, current.ID, &current, nil)
	return nil
}

// validate checks a record before it is written and returns it with its name fully qualified
// and its priority set for MX only. TTL 0 is automatic.
func (z *dnsZone) validate(record mailtransport.DNSRecord, priority *int) (mailtransport.DNSRecord, error) {
	if !editableDNSTypes[record.Type] {
		return record, invalidRecord("type", "must be one of A, AAAA, CNAME, TXT, MX")
	}
	name, err := qualifyDNSName(record.Name, z.zone.Name)
	if err != nil {
		return record, err
	}
	record.Name = name
	if err := validateDNSContent(record.Type, record.Content); err != nil {
		return record, err
	}
	record.Priority = 0
	if record.Type == "MX" {
		if priority == nil {
			return record, invalidRecord("priority", "is required for MX records")
		}
		if *priority < 0 || *priority > maxMXPriority {
			return record, invalidRecord("priority", "must be between 0 and 65535")
		}
		record.Priority = *priority
	}
	if record.Proxied && !proxiableDNSTypes[record.Type] {
		return record, invalidRecord("proxied", "is only allowed on A, AAAA, and CNAME records")
	}
	if record.TTL == 0 {
		record.TTL = automaticTTL
	}
	if record.TTL != automaticTTL && (record.TTL < minDNSTTL || record.TTL > maxDNSTTL) {
		return record, invalidRecord("ttl", "must be 1 (automatic) or between 60 and 86400")
	}
	if record.Type == "CNAME" && record.Name == normalizeDNSName(z.zone.Name) {
		return record, invalidRecord("name", "a CNAME record cannot be at the zone apex")
	}
	for _, other := range z.records {
		if other.ID == record.ID || normalizeDNSName(other.Name) != record.Name {
			continue
		}
		if record.Type == "CNAME" || strings.EqualFold(other.Type, "CNAME") {
			return record, invalidRecord("name", "a CNAME record cannot share its name with another record")
		}
	}
	return record, nil
}

// qualifyDNSName turns "@" into the zone name and a single label into a name in the zone. Any
// other name must be the zone name or end with "." and the zone name.
func qualifyDNSName(name, zoneName string) (string, error) {
	name = normalizeDNSName(name)
	zoneName = normalizeDNSName(zoneName)
	switch {
	case name == "":
		return "", invalidRecord("name", "is required")
	case name == "@":
		return zoneName, nil
	case !strings.Contains(name, "."):
		name += "." + zoneName
	case name != zoneName && !strings.HasSuffix(name, "."+zoneName):
		return "", invalidRecord("name", "must be in the zone "+zoneName)
	}
	if len(name) > maxDNSNameLength {
		return "", invalidRecord("name", "is too long")
	}
	for index, label := range strings.Split(name, ".") {
		if !validDNSLabel(label, index == 0) {
			return "", invalidRecord("name", "is not a valid DNS name")
		}
	}
	return name, nil
}

// validDNSLabel accepts letters, digits, hyphens, and underscores, and a lone "*" as the first
// label of a wildcard.
func validDNSLabel(label string, first bool) bool {
	if label == "*" {
		return first
	}
	if label == "" || len(label) > maxDNSLabelLength || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for _, char := range label {
		if !dnsLabelChar(char) {
			return false
		}
	}
	return true
}

func dnsLabelChar(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-', char == '_':
		return true
	}
	return false
}

func validateDNSContent(recordType, content string) error {
	if content == "" {
		return invalidRecord("content", "is required")
	}
	switch recordType {
	case "A":
		address, err := netip.ParseAddr(content)
		if err != nil || !address.Is4() {
			return invalidRecord("content", "must be an IPv4 address")
		}
	case "AAAA":
		address, err := netip.ParseAddr(content)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return invalidRecord("content", "must be an IPv6 address")
		}
	case "TXT":
		if utf8.RuneCountInString(content) > maxTXTContentLength {
			return invalidRecord("content", "must be at most 2048 characters")
		}
	case "CNAME", "MX":
		host := normalizeDNSName(content)
		if host == "" || len(host) > maxDNSNameLength || strings.ContainsAny(host, " \t") {
			return invalidRecord("content", "must be a host name")
		}
	}
	return nil
}

// recordWriteError turns Cloudflare's refusal of a write into ErrMailDNSRecordRejected with its
// reason, which is safe to show to the organization that owns the zone.
func recordWriteError(err error) error {
	var failure *mailtransport.DomainError
	if errors.As(err, &failure) {
		reason := failure.Message
		if reason == "" {
			reason = failure.Error()
		}
		return fmt.Errorf("%w: %s", ErrMailDNSRecordRejected, reason)
	}
	return providerError(err)
}

// auditDNSRecord enriches the request's audit entry with the zone, type, and name of the record
// before and after the write. Record content is never audited: it can hold verification tokens.
func (s *service) auditDNSRecord(ctx context.Context, action string, zone *domain.MailZone, recordID string, before, after *mailtransport.DNSRecord) {
	if s.recordAudit == nil {
		return
	}
	var zoneChange, typeChange, nameChange domain.AuditValueChange
	if before != nil {
		zoneChange.Before, typeChange.Before, nameChange.Before = zone.Name, before.Type, before.Name
	}
	if after != nil {
		zoneChange.After, typeChange.After, nameChange.After = zone.Name, after.Type, after.Name
	}
	changes := map[string]domain.AuditValueChange{"zone": zoneChange, "type": typeChange, "name": nameChange}
	s.recordAudit(ctx, auditstarter.Change{
		Action: action, Resource: "mail_dns_records", TargetType: "mail_dns_record", TargetID: recordID,
		Result: domain.AuditResultSuccess, Changes: changes,
	})
}
