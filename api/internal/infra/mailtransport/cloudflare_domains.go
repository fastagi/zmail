package mailtransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

// ErrZoneNotFound reports that no zone the credentials can manage contains the domain.
var ErrZoneNotFound = errors.New("no manageable zone contains the domain")

// ErrZoneExists reports that the provider already holds a zone for the name, in this account or
// another one.
var ErrZoneExists = errors.New("the zone already exists at the provider")

// ErrActivationCheckLimited reports that the provider refused an activation check because one
// ran too recently.
var ErrActivationCheckLimited = errors.New("the zone activation check is rate limited")

// maxDomainResponseBytes bounds one management response; a zone's record listing is the largest.
const maxDomainResponseBytes = 1024 * 1024

// Cloudflare error codes the client turns into sentinel errors.
const (
	cloudflareCodeZoneExists             = 1061
	cloudflareCodeActivationCheckLimited = 1224
)

// recordsPerPage and maxRecordPages bound a full zone listing to 10,000 records.
const (
	recordsPerPage = 100
	maxRecordPages = 100
)

// Zone is a DNS zone the provider manages.
type Zone struct {
	ID   string
	Name string
}

// ZoneDetails is a zone with its activation state. Status is the provider's own value, such as
// pending, active, or moved. NameServers are the ones the provider assigned; OriginalNameServers
// are the ones the domain used before it was added.
type ZoneDetails struct {
	ID                  string
	Name                string
	Status              string
	NameServers         []string
	OriginalNameServers []string
}

// DNSRecord is one DNS record. Name is a fully qualified name without a trailing dot. ID is the
// provider's record ID, empty for a record not written yet. TTL 1 means automatic.
type DNSRecord struct {
	ID       string
	Type     string
	Name     string
	Content  string
	Priority int
	TTL      int
	Proxied  bool
}

// DomainProvider manages zones in the provider account, their DNS records, and the email
// settings of their domains. It holds no business rules: callers decide what to create, change,
// or remove, and what counts as complete.
type DomainProvider interface {
	// FindZone returns the zone that contains name: the zone itself or the closest parent.
	FindZone(ctx context.Context, name string) (Zone, error)
	// EnableRouting turns on Email Routing for name, which adds its MX and SPF records.
	EnableRouting(ctx context.Context, zoneID, name string) error
	// EnsureSending turns on Email Sending for name when it is not on yet, and returns its tag.
	EnsureSending(ctx context.Context, zoneID, name string) (string, error)
	// SendingRecords lists the records Email Sending needs for the sending subdomain tag.
	SendingRecords(ctx context.Context, zoneID, tag string) ([]DNSRecord, error)
	// ListRecords lists the records of every type at exactly name.
	ListRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error)
	// CreateRecord writes one record and returns it with its provider ID.
	CreateRecord(ctx context.Context, zoneID string, record DNSRecord) (DNSRecord, error)
	// CreateRoutingRule routes mail for one address to the named Email Worker.
	CreateRoutingRule(ctx context.Context, zoneID, address, worker string) (string, error)
	DeleteRoutingRule(ctx context.Context, zoneID, ruleID string) error

	// CreateZone adds name to the account as a full zone. It reports ErrZoneExists when the
	// provider already holds the name.
	CreateZone(ctx context.Context, name string) (ZoneDetails, error)
	GetZone(ctx context.Context, zoneID string) (ZoneDetails, error)
	// RequestActivationCheck asks the provider to look for the assigned name servers now. It
	// reports ErrActivationCheckLimited when a check ran too recently.
	RequestActivationCheck(ctx context.Context, zoneID string) error
	DeleteZone(ctx context.Context, zoneID string) error
	// ScanRecords asks the provider to import the records the domain already publishes.
	ScanRecords(ctx context.Context, zoneID string) error
	// ListZoneRecords lists every record in the zone.
	ListZoneRecords(ctx context.Context, zoneID string) ([]DNSRecord, error)
	// UpdateRecord rewrites the record with record.ID.
	UpdateRecord(ctx context.Context, zoneID string, record DNSRecord) error
	DeleteRecord(ctx context.Context, zoneID, recordID string) error
}

// NewDomainProvider returns the Cloudflare domain client when Cloudflare is the configured
// provider with credentials, and a provider that reports ErrNotConfigured otherwise.
func NewDomainProvider(cfg *config.Config) DomainProvider {
	if cfg == nil {
		return unconfiguredDomains{}
	}
	mailbox := cfg.Mailbox.Resolved(cfg.IsProduction())
	if mailbox.Provider != config.MailboxProviderCloudflare || mailbox.CloudflareAPIToken == "" ||
		mailbox.CloudflareAccountID == "" {
		return unconfiguredDomains{}
	}
	return NewCloudflareDomains(mailbox)
}

// CloudflareDomains manages zones through the Cloudflare API.
type CloudflareDomains struct {
	baseURL   string
	accountID string
	token     string
	timeout   time.Duration
	client    httpDoer
}

var _ DomainProvider = (*CloudflareDomains)(nil)

// NewCloudflareDomains builds the client from resolved mailbox configuration.
func NewCloudflareDomains(cfg config.MailboxConfig) *CloudflareDomains {
	return newCloudflareDomains(cfg, nil, defaultCloudflareBaseURL)
}

func newCloudflareDomains(cfg config.MailboxConfig, client httpDoer, baseURL string) *CloudflareDomains {
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultMailboxRequestTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &CloudflareDomains{
		baseURL:   strings.TrimRight(baseURL, "/"),
		accountID: strings.TrimSpace(cfg.CloudflareAccountID),
		token:     strings.TrimSpace(cfg.CloudflareAPIToken),
		timeout:   timeout,
		client:    client,
	}
}

// DomainError is a failed provider call. Message is the provider's own explanation, safe to
// show to the organization that owns the domain.
type DomainError struct {
	Operation string
	Status    int
	Code      int
	Message   string
}

func (e *DomainError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("cloudflare %s failed: %s", e.Operation, e.Message)
	}
	return fmt.Sprintf("cloudflare %s failed with HTTP %d", e.Operation, e.Status)
}

type cloudflareEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage      `json:"result"`
	ResultInfo cloudflareResultInfo `json:"result_info"`
}

type cloudflareResultInfo struct {
	TotalPages int `json:"total_pages"`
}

func (c *CloudflareDomains) call(ctx context.Context, operation, method, path string, query url.Values, body, out any) error {
	_, err := c.callPaged(ctx, operation, method, path, query, body, out)
	return err
}

// callPaged is call that also returns the pagination details of a list response.
func (c *CloudflareDomains) callPaged(ctx context.Context, operation, method, path string, query url.Values, body, out any) (cloudflareResultInfo, error) {
	var none cloudflareResultInfo
	if c == nil || c.token == "" {
		return none, ErrNotConfigured
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return none, fmt.Errorf("encode %s request: %w", operation, err)
		}
		payload = bytes.NewReader(encoded)
	}
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, target, payload)
	if err != nil {
		return none, fmt.Errorf("build %s request: %w", operation, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return none, &DomainError{Operation: operation, Message: "the provider could not be reached"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxDomainResponseBytes+1))
	if err != nil || len(raw) > maxDomainResponseBytes {
		return none, &DomainError{Operation: operation, Status: response.StatusCode, Message: "the provider response could not be read"}
	}
	var envelope cloudflareEnvelope
	decodeErr := json.Unmarshal(raw, &envelope)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || decodeErr != nil || !envelope.Success {
		failure := &DomainError{Operation: operation, Status: response.StatusCode}
		if decodeErr == nil && len(envelope.Errors) > 0 {
			failure.Code = envelope.Errors[0].Code
			failure.Message = truncate(envelope.Errors[0].Message, 300)
		}
		return none, failure
	}
	if out != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return none, &DomainError{Operation: operation, Status: response.StatusCode, Message: "the provider response was not understood"}
		}
	}
	return envelope.ResultInfo, nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (c *CloudflareDomains) FindZone(ctx context.Context, name string) (Zone, error) {
	labels := strings.Split(strings.Trim(strings.ToLower(name), "."), ".")
	// Try the name itself, then each parent down to a two-label name.
	for start := 0; start+2 <= len(labels); start++ {
		candidate := strings.Join(labels[start:], ".")
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		query := url.Values{"name": {candidate}, "account.id": {c.accountID}}
		if err := c.call(ctx, "zone lookup", http.MethodGet, "/zones", query, nil, &zones); err != nil {
			return Zone{}, err
		}
		for _, zone := range zones {
			if strings.EqualFold(zone.Name, candidate) {
				return Zone{ID: zone.ID, Name: strings.ToLower(zone.Name)}, nil
			}
		}
	}
	return Zone{}, ErrZoneNotFound
}

func (c *CloudflareDomains) EnableRouting(ctx context.Context, zoneID, name string) error {
	return c.call(ctx, "enable email routing", http.MethodPost,
		"/zones/"+url.PathEscape(zoneID)+"/email/routing/dns", nil, map[string]string{"name": name}, nil)
}

type cloudflareSendingSubdomain struct {
	// ID identifies the sending domain; older responses call it tag.
	ID      string `json:"id"`
	Tag     string `json:"tag"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (s cloudflareSendingSubdomain) identifier() string {
	if s.ID != "" {
		return s.ID
	}
	return s.Tag
}

func (c *CloudflareDomains) EnsureSending(ctx context.Context, zoneID, name string) (string, error) {
	path := "/zones/" + url.PathEscape(zoneID) + "/email/sending/subdomains"
	var existing []cloudflareSendingSubdomain
	if err := c.call(ctx, "list sending domains", http.MethodGet, path, nil, nil, &existing); err != nil {
		return "", err
	}
	for _, subdomain := range existing {
		if strings.EqualFold(subdomain.Name, name) && subdomain.identifier() != "" {
			return subdomain.identifier(), nil
		}
	}
	var created cloudflareSendingSubdomain
	if err := c.call(ctx, "enable email sending", http.MethodPost, path, nil, map[string]string{"name": name}, &created); err != nil {
		return "", err
	}
	if created.identifier() == "" {
		return "", &DomainError{Operation: "enable email sending", Message: "the provider returned no sending domain"}
	}
	return created.identifier(), nil
}

type cloudflareRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority *int   `json:"priority,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
}

func (r cloudflareRecord) toRecord(zoneName string) DNSRecord {
	record := DNSRecord{ID: r.ID, Type: strings.ToUpper(r.Type), Name: qualify(r.Name, zoneName), Content: r.Content, TTL: r.TTL}
	if r.Priority != nil {
		record.Priority = *r.Priority
	}
	if r.Proxied != nil {
		record.Proxied = *r.Proxied
	}
	return record
}

// recordBody is the write body for record; MX records carry their priority.
func recordBody(record DNSRecord) cloudflareRecord {
	body := cloudflareRecord{Type: record.Type, Name: record.Name, Content: record.Content, TTL: record.TTL}
	if body.TTL <= 0 {
		body.TTL = 1
	}
	if strings.EqualFold(record.Type, "MX") {
		priority := record.Priority
		body.Priority = &priority
	}
	return body
}

// qualify turns a relative record name, or "@", into a fully qualified name of the zone.
func qualify(name, zoneName string) string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	zoneName = strings.ToLower(zoneName)
	switch {
	case name == "" || name == "@":
		return zoneName
	case zoneName == "" || name == zoneName || strings.HasSuffix(name, "."+zoneName):
		return name
	default:
		return name + "." + zoneName
	}
}

func (c *CloudflareDomains) zoneName(ctx context.Context, zoneID string) (string, error) {
	var zone struct {
		Name string `json:"name"`
	}
	if err := c.call(ctx, "zone lookup", http.MethodGet, "/zones/"+url.PathEscape(zoneID), nil, nil, &zone); err != nil {
		return "", err
	}
	return zone.Name, nil
}

func (c *CloudflareDomains) SendingRecords(ctx context.Context, zoneID, tag string) ([]DNSRecord, error) {
	var rows []cloudflareRecord
	path := "/zones/" + url.PathEscape(zoneID) + "/email/sending/subdomains/" + url.PathEscape(tag) + "/dns"
	if err := c.call(ctx, "sending records", http.MethodGet, path, nil, nil, &rows); err != nil {
		return nil, err
	}
	zoneName, err := c.zoneName(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	records := make([]DNSRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.toRecord(zoneName))
	}
	return records, nil
}

func (c *CloudflareDomains) ListRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	var rows []cloudflareRecord
	query := url.Values{"name": {name}, "per_page": {"100"}}
	if err := c.call(ctx, "list records", http.MethodGet, recordsPath(zoneID), query, nil, &rows); err != nil {
		return nil, err
	}
	records := make([]DNSRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.toRecord(""))
	}
	return records, nil
}

func (c *CloudflareDomains) CreateRecord(ctx context.Context, zoneID string, record DNSRecord) (DNSRecord, error) {
	body := recordBody(record)
	var created cloudflareRecord
	if err := c.call(ctx, "create record", http.MethodPost, recordsPath(zoneID), nil, body, &created); err != nil {
		return DNSRecord{}, err
	}
	if created.ID == "" {
		return DNSRecord{}, &DomainError{Operation: "create record", Message: "the provider returned no record"}
	}
	// The written values are authoritative; the provider adds the ID and its own settings.
	record.ID = created.ID
	record.TTL = body.TTL
	if created.TTL > 0 {
		record.TTL = created.TTL
	}
	if created.Proxied != nil {
		record.Proxied = *created.Proxied
	}
	return record, nil
}

func recordsPath(zoneID string) string {
	return "/zones/" + url.PathEscape(zoneID) + "/dns_records"
}

func (c *CloudflareDomains) ListZoneRecords(ctx context.Context, zoneID string) ([]DNSRecord, error) {
	var records []DNSRecord
	for page := 1; ; page++ {
		if page > maxRecordPages {
			return nil, &DomainError{Operation: "list zone records", Message: "the zone has too many records"}
		}
		var rows []cloudflareRecord
		query := url.Values{"per_page": {strconv.Itoa(recordsPerPage)}, "page": {strconv.Itoa(page)}}
		info, err := c.callPaged(ctx, "list zone records", http.MethodGet, recordsPath(zoneID), query, nil, &rows)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			records = append(records, row.toRecord(""))
		}
		if page >= info.TotalPages || len(rows) == 0 {
			return records, nil
		}
	}
}

func (c *CloudflareDomains) UpdateRecord(ctx context.Context, zoneID string, record DNSRecord) error {
	if record.ID == "" {
		return &DomainError{Operation: "update record", Message: "the record has no provider ID"}
	}
	body := recordBody(record)
	proxied := record.Proxied
	body.Proxied = &proxied
	return c.call(ctx, "update record", http.MethodPatch, recordsPath(zoneID)+"/"+url.PathEscape(record.ID), nil, body, nil)
}

func (c *CloudflareDomains) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	return c.call(ctx, "delete record", http.MethodDelete, recordsPath(zoneID)+"/"+url.PathEscape(recordID), nil, nil, nil)
}

func (c *CloudflareDomains) ScanRecords(ctx context.Context, zoneID string) error {
	return c.call(ctx, "scan records", http.MethodPost, recordsPath(zoneID)+"/scan", nil, nil, nil)
}

type cloudflareZone struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Status              string   `json:"status"`
	NameServers         []string `json:"name_servers"`
	OriginalNameServers []string `json:"original_name_servers"`
}

func (z cloudflareZone) toDetails() ZoneDetails {
	return ZoneDetails{
		ID:                  z.ID,
		Name:                strings.ToLower(z.Name),
		Status:              z.Status,
		NameServers:         z.NameServers,
		OriginalNameServers: z.OriginalNameServers,
	}
}

type cloudflareZoneAccount struct {
	ID string `json:"id"`
}

type cloudflareCreateZone struct {
	Name    string                `json:"name"`
	Account cloudflareZoneAccount `json:"account"`
	Type    string                `json:"type"`
}

func (c *CloudflareDomains) CreateZone(ctx context.Context, name string) (ZoneDetails, error) {
	body := cloudflareCreateZone{Name: name, Account: cloudflareZoneAccount{ID: c.accountID}, Type: "full"}
	var zone cloudflareZone
	if err := c.call(ctx, "create zone", http.MethodPost, "/zones", nil, body, &zone); err != nil {
		var failure *DomainError
		if errors.As(err, &failure) && failure.Code == cloudflareCodeZoneExists {
			return ZoneDetails{}, fmt.Errorf("%w: %w", ErrZoneExists, err)
		}
		return ZoneDetails{}, err
	}
	if zone.ID == "" {
		return ZoneDetails{}, &DomainError{Operation: "create zone", Message: "the provider returned no zone"}
	}
	return zone.toDetails(), nil
}

func (c *CloudflareDomains) GetZone(ctx context.Context, zoneID string) (ZoneDetails, error) {
	var zone cloudflareZone
	if err := c.call(ctx, "get zone", http.MethodGet, "/zones/"+url.PathEscape(zoneID), nil, nil, &zone); err != nil {
		return ZoneDetails{}, err
	}
	return zone.toDetails(), nil
}

func (c *CloudflareDomains) RequestActivationCheck(ctx context.Context, zoneID string) error {
	err := c.call(ctx, "activation check", http.MethodPut, "/zones/"+url.PathEscape(zoneID)+"/activation_check", nil, nil, nil)
	var failure *DomainError
	if errors.As(err, &failure) &&
		(failure.Status == http.StatusTooManyRequests || failure.Code == cloudflareCodeActivationCheckLimited) {
		return fmt.Errorf("%w: %w", ErrActivationCheckLimited, err)
	}
	return err
}

func (c *CloudflareDomains) DeleteZone(ctx context.Context, zoneID string) error {
	return c.call(ctx, "delete zone", http.MethodDelete, "/zones/"+url.PathEscape(zoneID), nil, nil, nil)
}

type cloudflareMatcher struct {
	Type  string `json:"type"`
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}

type cloudflareAction struct {
	Type  string   `json:"type"`
	Value []string `json:"value,omitempty"`
}

type cloudflareRule struct {
	ID       string              `json:"id,omitempty"`
	Name     string              `json:"name"`
	Enabled  bool                `json:"enabled"`
	Matchers []cloudflareMatcher `json:"matchers"`
	Actions  []cloudflareAction  `json:"actions"`
}

func (c *CloudflareDomains) CreateRoutingRule(ctx context.Context, zoneID, address, worker string) (string, error) {
	rule := cloudflareRule{
		Name:     "Zmail " + address,
		Enabled:  true,
		Matchers: []cloudflareMatcher{{Type: "literal", Field: "to", Value: address}},
		Actions:  []cloudflareAction{{Type: "worker", Value: []string{worker}}},
	}
	var created cloudflareRule
	if err := c.call(ctx, "create routing rule", http.MethodPost,
		"/zones/"+url.PathEscape(zoneID)+"/email/routing/rules", nil, rule, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", &DomainError{Operation: "create routing rule", Message: "the provider returned no rule"}
	}
	return created.ID, nil
}

func (c *CloudflareDomains) DeleteRoutingRule(ctx context.Context, zoneID, ruleID string) error {
	return c.call(ctx, "delete routing rule", http.MethodDelete,
		"/zones/"+url.PathEscape(zoneID)+"/email/routing/rules/"+url.PathEscape(ruleID), nil, nil, nil)
}

type unconfiguredDomains struct{}

func (unconfiguredDomains) FindZone(context.Context, string) (Zone, error) {
	return Zone{}, ErrNotConfigured
}
func (unconfiguredDomains) EnableRouting(context.Context, string, string) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) EnsureSending(context.Context, string, string) (string, error) {
	return "", ErrNotConfigured
}
func (unconfiguredDomains) SendingRecords(context.Context, string, string) ([]DNSRecord, error) {
	return nil, ErrNotConfigured
}
func (unconfiguredDomains) ListRecords(context.Context, string, string) ([]DNSRecord, error) {
	return nil, ErrNotConfigured
}
func (unconfiguredDomains) CreateRecord(context.Context, string, DNSRecord) (DNSRecord, error) {
	return DNSRecord{}, ErrNotConfigured
}
func (unconfiguredDomains) CreateRoutingRule(context.Context, string, string, string) (string, error) {
	return "", ErrNotConfigured
}
func (unconfiguredDomains) DeleteRoutingRule(context.Context, string, string) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) CreateZone(context.Context, string) (ZoneDetails, error) {
	return ZoneDetails{}, ErrNotConfigured
}
func (unconfiguredDomains) GetZone(context.Context, string) (ZoneDetails, error) {
	return ZoneDetails{}, ErrNotConfigured
}
func (unconfiguredDomains) RequestActivationCheck(context.Context, string) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) DeleteZone(context.Context, string) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) ScanRecords(context.Context, string) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) ListZoneRecords(context.Context, string) ([]DNSRecord, error) {
	return nil, ErrNotConfigured
}
func (unconfiguredDomains) UpdateRecord(context.Context, string, DNSRecord) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) DeleteRecord(context.Context, string, string) error {
	return ErrNotConfigured
}
