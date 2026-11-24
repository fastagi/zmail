package externalmail

import (
	"errors"
	"strings"
	"sync"
)

// AuthStrategy identifies how Zmail authenticates against an external mail provider.
type AuthStrategy string

const (
	AuthStrategyOAuth2      AuthStrategy = "oauth2"
	AuthStrategyAppPassword AuthStrategy = "app_password"
	AuthStrategyPassword    AuthStrategy = "password"
)

// TLSSecurity identifies the transport security mode for IMAP or SMTP connections.
type TLSSecurity string

const (
	TLSModeImplicit TLSSecurity = "tls"
	TLSModeSTARTTLS TLSSecurity = "starttls"
)

// ErrInvalidProviderStrategy is returned when registering a malformed strategy.
var ErrInvalidProviderStrategy = errors.New("externalmail: invalid provider strategy")

// ServerEndpoint holds host, port, and TLS mode for an IMAP or SMTP server.
type ServerEndpoint struct {
	Host string      `json:"host"`
	Port int         `json:"port"`
	TLS  TLSSecurity `json:"tls"`
}

// ProviderCapabilities describes the features supported by a mail provider preset.
type ProviderCapabilities struct {
	Read        bool `json:"read"`
	Send        bool `json:"send"`
	Push        bool `json:"push"`
	Idle        bool `json:"idle"`
	FolderSync  bool `json:"folder_sync"`
	OAuth2      bool `json:"oauth2"`
	AppPassword bool `json:"app_password"`
}

// ProviderSpec defines connection metadata and auth requirements for one provider.
type ProviderSpec struct {
	ID           string               `json:"id"`
	DisplayName  string               `json:"display_name"`
	Region       string               `json:"region"`
	Enterprise   bool                 `json:"enterprise"`
	Domains      []string             `json:"domains"`
	AuthStrategy AuthStrategy         `json:"auth_strategy"`
	IMAP         ServerEndpoint       `json:"imap"`
	SMTP         ServerEndpoint       `json:"smtp"`
	Capabilities ProviderCapabilities `json:"capabilities"`
	HelpURL      string               `json:"help_url"`
}

func (s ProviderSpec) clone() ProviderSpec {
	out := s
	if len(s.Domains) > 0 {
		out.Domains = make([]string, len(s.Domains))
		copy(out.Domains, s.Domains)
	}
	return out
}

// DetectResult contains the auto-discovered provider configuration for an email address.
type DetectResult struct {
	Email               string               `json:"email"`
	Domain              string               `json:"domain"`
	Valid               bool                 `json:"valid"`
	Matched             bool                 `json:"matched"`
	ProviderID          string               `json:"provider_id"`
	DisplayName         string               `json:"display_name"`
	AuthStrategy        AuthStrategy         `json:"auth_strategy"`
	RequiresOAuth2      bool                 `json:"requires_oauth2"`
	RequiresAppPassword bool                 `json:"requires_app_password"`
	IMAP                ServerEndpoint       `json:"imap"`
	SMTP                ServerEndpoint       `json:"smtp"`
	Capabilities        ProviderCapabilities `json:"capabilities"`
	HelpURL             string               `json:"help_url"`
}

// DiscoveryStrategy resolves connection settings for a normalized email domain.
type DiscoveryStrategy interface {
	ID() string
	Spec() ProviderSpec
	Match(domain string) bool
	Resolve(email, domain string) DetectResult
}

type domainEndpoints struct {
	IMAP ServerEndpoint
	SMTP ServerEndpoint
}

type presetStrategy struct {
	spec      ProviderSpec
	domains   map[string]struct{}
	overrides map[string]domainEndpoints
}

func newPresetStrategy(spec ProviderSpec, overrides map[string]domainEndpoints) *presetStrategy {
	domainSet := make(map[string]struct{}, len(spec.Domains))
	normalized := make([]string, 0, len(spec.Domains))
	for _, d := range spec.Domains {
		clean := strings.ToLower(strings.TrimSpace(d))
		if clean == "" {
			continue
		}
		if _, exists := domainSet[clean]; !exists {
			domainSet[clean] = struct{}{}
			normalized = append(normalized, clean)
		}
	}
	spec.Domains = normalized
	return &presetStrategy{spec: spec, domains: domainSet, overrides: overrides}
}

func (s *presetStrategy) ID() string         { return s.spec.ID }
func (s *presetStrategy) Spec() ProviderSpec { return s.spec.clone() }
func (s *presetStrategy) Match(domain string) bool {
	_, ok := s.domains[domain]
	return ok
}

func buildDetectResult(spec ProviderSpec, email, domain string, matched bool, imapEp, smtpEp ServerEndpoint) DetectResult {
	return DetectResult{
		Email:               email,
		Domain:              domain,
		Valid:               true,
		Matched:             matched,
		ProviderID:          spec.ID,
		DisplayName:         spec.DisplayName,
		AuthStrategy:        spec.AuthStrategy,
		RequiresOAuth2:      spec.AuthStrategy == AuthStrategyOAuth2,
		RequiresAppPassword: spec.AuthStrategy == AuthStrategyAppPassword,
		IMAP:                imapEp,
		SMTP:                smtpEp,
		Capabilities:        spec.Capabilities,
		HelpURL:             spec.HelpURL,
	}
}

func (s *presetStrategy) Resolve(email, domain string) DetectResult {
	imapEp, smtpEp := s.spec.IMAP, s.spec.SMTP
	if ov, ok := s.overrides[domain]; ok {
		imapEp, smtpEp = ov.IMAP, ov.SMTP
	}
	return buildDetectResult(s.spec, email, domain, true, imapEp, smtpEp)
}

type genericIMAPStrategy struct {
	spec ProviderSpec
}

func newGenericIMAPStrategy() *genericIMAPStrategy {
	return &genericIMAPStrategy{
		spec: ProviderSpec{
			ID: "imap", DisplayName: "Custom IMAP / SMTP", Region: "global", Enterprise: true,
			AuthStrategy: AuthStrategyPassword,
			IMAP:         ServerEndpoint{Port: 993, TLS: TLSModeImplicit},
			SMTP:         ServerEndpoint{Port: 465, TLS: TLSModeImplicit},
			Capabilities: ProviderCapabilities{Read: true, Send: true, Idle: true, FolderSync: true, AppPassword: true},
			HelpURL:      "https://datatracker.ietf.org/doc/html/rfc9051",
		},
	}
}

func (s *genericIMAPStrategy) ID() string          { return s.spec.ID }
func (s *genericIMAPStrategy) Spec() ProviderSpec  { return s.spec.clone() }
func (s *genericIMAPStrategy) Match(_ string) bool { return false }
func (s *genericIMAPStrategy) Resolve(email, domain string) DetectResult {
	imapEp := ServerEndpoint{Host: "imap." + domain, Port: s.spec.IMAP.Port, TLS: s.spec.IMAP.TLS}
	smtpEp := ServerEndpoint{Host: "smtp." + domain, Port: s.spec.SMTP.Port, TLS: s.spec.SMTP.TLS}
	return buildDetectResult(s.spec, email, domain, false, imapEp, smtpEp)
}

// ProviderRegistry stores external mail provider discovery strategies in deterministic order.
type ProviderRegistry struct {
	mu       sync.RWMutex
	order    []string
	byID     map[string]DiscoveryStrategy
	byDomain map[string]string
	fallback DiscoveryStrategy
}

// NewProviderRegistry constructs a registry pre-loaded with all built-in provider presets.
func NewProviderRegistry() *ProviderRegistry {
	fallback := newGenericIMAPStrategy()
	reg := &ProviderRegistry{
		byID:     make(map[string]DiscoveryStrategy, 16),
		byDomain: make(map[string]string, 32),
		fallback: fallback,
	}
	for _, s := range builtinStrategies() {
		_ = reg.Register(s)
	}
	_ = reg.Register(fallback)
	return reg
}

// Register adds or replaces a provider discovery strategy in the registry.
func (r *ProviderRegistry) Register(strategy DiscoveryStrategy) error {
	if strategy == nil {
		return ErrInvalidProviderStrategy
	}
	id := strings.ToLower(strings.TrimSpace(strategy.ID()))
	if id == "" {
		return ErrInvalidProviderStrategy
	}
	spec := strategy.Spec()

	r.mu.Lock()
	defer r.mu.Unlock()

	if prev, exists := r.byID[id]; exists {
		for _, oldDomain := range prev.Spec().Domains {
			if r.byDomain[oldDomain] == id {
				delete(r.byDomain, oldDomain)
			}
		}
	} else {
		r.order = append(r.order, id)
	}

	r.byID[id] = strategy
	for _, d := range spec.Domains {
		clean := strings.ToLower(strings.TrimSpace(d))
		if clean != "" {
			r.byDomain[clean] = id
		}
	}
	return nil
}

// Get returns the provider specification for a given provider ID.
func (r *ProviderRegistry) Get(id string) (ProviderSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	r.mu.RLock()
	strategy, ok := r.byID[key]
	r.mu.RUnlock()
	if !ok {
		return ProviderSpec{}, false
	}
	return strategy.Spec(), true
}

// ListPresets returns all registered provider specifications in deterministic order.
func (r *ProviderRegistry) ListPresets() []ProviderSpec {
	r.mu.RLock()
	specs := make([]ProviderSpec, 0, len(r.order))
	for _, id := range r.order {
		if strategy, ok := r.byID[id]; ok {
			specs = append(specs, strategy.Spec())
		}
	}
	r.mu.RUnlock()
	return specs
}

// DetectProvider normalizes the email, matches a known provider preset, or falls back to generic IMAP.
func (r *ProviderRegistry) DetectProvider(email string) DetectResult {
	normalized, domain, ok := parseEmailAddress(email)
	if !ok {
		return DetectResult{Email: normalized, Valid: false}
	}

	r.mu.RLock()
	var strategy DiscoveryStrategy
	if id, found := r.byDomain[domain]; found {
		strategy = r.byID[id]
	}
	if strategy == nil {
		for _, id := range r.order {
			candidate := r.byID[id]
			if candidate != nil && candidate.Match(domain) {
				strategy = candidate
				break
			}
		}
	}
	fallback := r.fallback
	r.mu.RUnlock()

	if strategy != nil {
		return strategy.Resolve(normalized, domain)
	}
	return fallback.Resolve(normalized, domain)
}

var defaultProviderRegistry = NewProviderRegistry()

// DetectProvider resolves the recommended provider preset for an email address using the default registry.
func DetectProvider(email string) DetectResult {
	return defaultProviderRegistry.DetectProvider(email)
}

// ListPresets returns all built-in email provider presets in deterministic order.
func ListPresets() []ProviderSpec {
	return defaultProviderRegistry.ListPresets()
}

func parseEmailAddress(raw string) (string, string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" || strings.ContainsAny(normalized, " \t\r\n") {
		return normalized, "", false
	}
	at := strings.IndexByte(normalized, '@')
	if at <= 0 || at != strings.LastIndexByte(normalized, '@') || at == len(normalized)-1 {
		return normalized, "", false
	}
	local, domain := normalized[:at], normalized[at+1:]
	if len(local) > 64 || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return normalized, "", false
	}
	if !isValidDomain(domain) {
		return normalized, "", false
	}
	return normalized, domain, true
}

func isValidDomain(domain string) bool {
	if len(domain) < 4 || len(domain) > 253 || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 || len(labels[len(labels)-1]) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	return true
}

func tlsEP(host string, port int) ServerEndpoint {
	return ServerEndpoint{Host: host, Port: port, TLS: TLSModeImplicit}
}

func starttlsEP(host string, port int) ServerEndpoint {
	return ServerEndpoint{Host: host, Port: port, TLS: TLSModeSTARTTLS}
}

func builtinStrategies() []DiscoveryStrategy {
	oauthCaps := ProviderCapabilities{Read: true, Send: true, Push: true, Idle: true, FolderSync: true, OAuth2: true}
	appPwdCaps := ProviderCapabilities{Read: true, Send: true, Idle: true, FolderSync: true, AppPassword: true}
	neteaseOverrides := map[string]domainEndpoints{
		"126.com":  {IMAP: tlsEP("imap.126.com", 993), SMTP: tlsEP("smtp.126.com", 465)},
		"yeah.net": {IMAP: tlsEP("imap.yeah.net", 993), SMTP: tlsEP("smtp.yeah.net", 465)},
	}
	preset := func(id, name, region string, ent bool, domains []string, auth AuthStrategy, imap, smtp ServerEndpoint, help string, ov map[string]domainEndpoints) DiscoveryStrategy {
		caps := appPwdCaps
		if auth == AuthStrategyOAuth2 {
			caps = oauthCaps
		}
		return newPresetStrategy(ProviderSpec{
			ID: id, DisplayName: name, Region: region, Enterprise: ent,
			Domains: domains, AuthStrategy: auth, IMAP: imap, SMTP: smtp,
			Capabilities: caps, HelpURL: help,
		}, ov)
	}
	return []DiscoveryStrategy{
		preset("gmail", "Gmail", "global", false, []string{"gmail.com", "googlemail.com"}, AuthStrategyOAuth2, tlsEP("imap.gmail.com", 993), tlsEP("smtp.gmail.com", 465), "https://support.google.com/mail/answer/7126229", nil),
		preset("outlook", "Outlook / Microsoft 365", "global", false, []string{"outlook.com", "hotmail.com", "live.com", "msn.com"}, AuthStrategyOAuth2, tlsEP("outlook.office365.com", 993), starttlsEP("smtp.office365.com", 587), "https://support.microsoft.com/en-us/office/pop-imap-and-smtp-settings-for-outlook-com-d088b986-291d-42b8-9564-9c414e2aa040", nil),
		preset("icloud", "iCloud Mail", "global", false, []string{"icloud.com", "me.com", "mac.com"}, AuthStrategyAppPassword, tlsEP("imap.mail.me.com", 993), starttlsEP("smtp.mail.me.com", 587), "https://support.apple.com/en-us/102654", nil),
		preset("qq", "QQ Mail", "cn", false, []string{"qq.com", "foxmail.com"}, AuthStrategyAppPassword, tlsEP("imap.qq.com", 993), tlsEP("smtp.qq.com", 465), "https://service.mail.qq.com/detail/0/75", nil),
		preset("netease163", "NetEase 163 / 126 Mail", "cn", false, []string{"163.com", "126.com", "yeah.net"}, AuthStrategyAppPassword, tlsEP("imap.163.com", 993), tlsEP("smtp.163.com", 465), "https://help.mail.163.com/", neteaseOverrides),
		preset("fastmail", "Fastmail", "global", false, []string{"fastmail.com", "fastmail.fm"}, AuthStrategyAppPassword, tlsEP("imap.fastmail.com", 993), tlsEP("smtp.fastmail.com", 465), "https://www.fastmail.help/hc/en-us/articles/1500000278342-Server-names-and-ports", nil),
		preset("yahoo", "Yahoo Mail", "global", false, []string{"yahoo.com", "ymail.com"}, AuthStrategyAppPassword, tlsEP("imap.mail.yahoo.com", 993), tlsEP("smtp.mail.yahoo.com", 465), "https://help.yahoo.com/kb/SLN15241.html", nil),
		preset("zoho", "Zoho Mail", "global", true, []string{"zoho.com", "zohomail.com"}, AuthStrategyAppPassword, tlsEP("imap.zoho.com", 993), tlsEP("smtp.zoho.com", 465), "https://www.zoho.com/mail/help/imap-access.html", nil),
		preset("exmail", "Tencent Exmail", "cn", true, []string{"exmail.qq.com"}, AuthStrategyAppPassword, tlsEP("imap.exmail.qq.com", 993), tlsEP("smtp.exmail.qq.com", 465), "https://service.exmail.qq.com/cgi-bin/help?subtype=1&id=28&no=1000585", nil),
		preset("feishu", "Feishu Mail", "cn", true, []string{"feishu.cn", "larksuite.com"}, AuthStrategyAppPassword, tlsEP("imap.feishu.cn", 993), tlsEP("smtp.feishu.cn", 465), "https://www.feishu.cn/hc/en-US/articles/360049067887", nil),
		preset("aliyun", "Alibaba Mail", "cn", true, []string{"aliyun.com"}, AuthStrategyAppPassword, tlsEP("imap.aliyun.com", 993), tlsEP("smtp.aliyun.com", 465), "https://help.aliyun.com/document_detail/36576.html", nil),
		preset("sina", "Sina Mail", "cn", false, []string{"sina.com", "sina.cn"}, AuthStrategyAppPassword, tlsEP("imap.sina.com", 993), tlsEP("smtp.sina.com", 465), "https://help.sina.com.cn/", nil),
	}
}
