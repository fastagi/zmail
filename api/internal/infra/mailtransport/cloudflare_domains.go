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
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

// ErrZoneNotFound reports that no zone the credentials can manage contains the domain.
var ErrZoneNotFound = errors.New("no manageable zone contains the domain")

// maxDomainResponseBytes bounds one management response; a zone's record listing is the largest.
const maxDomainResponseBytes = 1024 * 1024

// Zone is a DNS zone the provider manages.
type Zone struct {
	ID   string
	Name string
}

// DNSRecord is one DNS record. Name is a fully qualified name without a trailing dot.
type DNSRecord struct {
	Type     string
	Name     string
	Content  string
	Priority int
}

// DomainProvider sets up and inspects the email settings of domains in the provider account. It
// holds no business rules: callers decide what to create and what counts as complete. It only
// ever adds records and rules; it never edits or deletes records it did not create.
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
	CreateRecord(ctx context.Context, zoneID string, record DNSRecord) error
	// CreateRoutingRule routes mail for one address to the named Email Worker.
	CreateRoutingRule(ctx context.Context, zoneID, address, worker string) (string, error)
	DeleteRoutingRule(ctx context.Context, zoneID, ruleID string) error
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
	Result json.RawMessage `json:"result"`
}

func (c *CloudflareDomains) call(ctx context.Context, operation, method, path string, query url.Values, body, out any) error {
	if c == nil || c.token == "" {
		return ErrNotConfigured
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", operation, err)
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
		return fmt.Errorf("build %s request: %w", operation, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return &DomainError{Operation: operation, Message: "the provider could not be reached"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxDomainResponseBytes+1))
	if err != nil || len(raw) > maxDomainResponseBytes {
		return &DomainError{Operation: operation, Status: response.StatusCode, Message: "the provider response could not be read"}
	}
	var envelope cloudflareEnvelope
	decodeErr := json.Unmarshal(raw, &envelope)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || decodeErr != nil || !envelope.Success {
		failure := &DomainError{Operation: operation, Status: response.StatusCode}
		if decodeErr == nil && len(envelope.Errors) > 0 {
			failure.Code = envelope.Errors[0].Code
			failure.Message = truncate(envelope.Errors[0].Message, 300)
		}
		return failure
	}
	if out != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return &DomainError{Operation: operation, Status: response.StatusCode, Message: "the provider response was not understood"}
		}
	}
	return nil
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
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority *int   `json:"priority,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
}

func (r cloudflareRecord) toRecord(zoneName string) DNSRecord {
	record := DNSRecord{Type: strings.ToUpper(r.Type), Name: qualify(r.Name, zoneName), Content: r.Content}
	if r.Priority != nil {
		record.Priority = *r.Priority
	}
	return record
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
	if err := c.call(ctx, "list records", http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records", query, nil, &rows); err != nil {
		return nil, err
	}
	records := make([]DNSRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.toRecord(""))
	}
	return records, nil
}

func (c *CloudflareDomains) CreateRecord(ctx context.Context, zoneID string, record DNSRecord) error {
	body := cloudflareRecord{Type: record.Type, Name: record.Name, Content: record.Content, TTL: 1}
	if record.Type == "MX" {
		priority := record.Priority
		body.Priority = &priority
	}
	return c.call(ctx, "create record", http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", nil, body, nil)
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
func (unconfiguredDomains) CreateRecord(context.Context, string, DNSRecord) error {
	return ErrNotConfigured
}
func (unconfiguredDomains) CreateRoutingRule(context.Context, string, string, string) (string, error) {
	return "", ErrNotConfigured
}
func (unconfiguredDomains) DeleteRoutingRule(context.Context, string, string) error {
	return ErrNotConfigured
}
