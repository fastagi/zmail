package mailbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

type CloudflareConnection struct {
	ID             string `json:"id" gorm:"type:uuid;primaryKey"`
	OrganizationID uint   `json:"-"`
	AccountID      string `json:"account_id"`
	ZoneID         string `json:"zone_id"`
	ZoneName       string `json:"zone_name"`
	Worker         string `json:"worker"`
	TokenCipher    string `json:"-"`
	SigningCipher  string `json:"-"`
	Active         bool   `json:"-"`
}

func (CloudflareConnection) TableName() string { return "mail_cloudflare_connections" }

type CloudflareConnectionService interface {
	CheckCloudflareAccess(context.Context, uint, string) (mailtransport.CloudflareAccessChecks, error)
	CloudflareZones(context.Context, string) ([]mailtransport.AccessibleZone, error)
	GetCloudflareConnection(context.Context, uint) (*CloudflareConnection, error)
	BindCloudflare(context.Context, uint, string, string) (*CloudflareConnection, string, error)
	UnbindCloudflare(context.Context, uint, string) error
	ConnectCloudflareDomain(context.Context, uint, uint, string, string, domain.MailDomainCapability) (*domain.MailDomain, error)
}
type cloudflareStore interface {
	findConnection(context.Context, uint, string) (*CloudflareConnection, error)
	insertConnection(context.Context, *CloudflareConnection) error
	revokeConnection(context.Context, uint, string) error
}

func (r *repository) findConnection(ctx context.Context, org uint, id string) (*CloudflareConnection, error) {
	var row CloudflareConnection
	q := r.conn(ctx).Where("active = true")
	if org != 0 {
		q = q.Where("organization_id = ?", org)
	}
	if id != "" {
		q = q.Where("id = ?", id)
	}
	err := q.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}
func (r *repository) insertConnection(ctx context.Context, row *CloudflareConnection) error {
	err := r.conn(ctx).Create(row).Error
	if isUniqueViolation(err) {
		return ErrMailDomainTaken
	}
	return err
}
func (r *repository) revokeConnection(ctx context.Context, org uint, id string) error {
	res := r.conn(ctx).Model(&CloudflareConnection{}).Where("organization_id = ? AND id = ? AND active", org, id).Updates(map[string]any{"active": false, "token_cipher": "", "signing_cipher": ""})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrMailDomainNotFound
	}
	return nil
}
func (s *service) connection(ctx context.Context, org uint, id string) (*CloudflareConnection, error) {
	store, ok := s.repo.(cloudflareStore)
	if !ok || s.encryptor == nil {
		return nil, ErrMailDomainsDisabled
	}
	return store.findConnection(ctx, org, id)
}
func (s *service) GetCloudflareConnection(ctx context.Context, org uint) (*CloudflareConnection, error) {
	return s.connection(ctx, org, "")
}
func (s *service) CloudflareZones(ctx context.Context, token string) ([]mailtransport.AccessibleZone, error) {
	return mailtransport.NewCloudflareDomains(config.MailboxConfig{CloudflareAPIToken: token}).AccessibleZones(ctx)
}
func (s *service) BindCloudflare(ctx context.Context, org uint, token, zoneID string) (*CloudflareConnection, string, error) {
	store, ok := s.repo.(cloudflareStore)
	if !ok {
		return nil, "", ErrMailDomainsDisabled
	}
	current, err := s.GetCloudflareConnection(ctx, org)
	if err != nil {
		return nil, "", err
	}
	if current != nil {
		return nil, "", ErrMailDomainTaken
	}
	zones, err := s.CloudflareZones(ctx, token)
	if err != nil {
		return nil, "", ErrMailDomainProviderFailed
	}
	for _, z := range zones {
		if z.ID != zoneID || z.AccountID == "" {
			continue
		}
		id := uuid.NewString()
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return nil, "", err
		}
		secret := base64.RawURLEncoding.EncodeToString(raw)
		row := &CloudflareConnection{ID: id, OrganizationID: org, AccountID: z.AccountID, ZoneID: z.ID, ZoneName: z.Name, Worker: "zmail-" + id, Active: true}
		if row.TokenCipher, err = s.encryptor.EncryptString(token); err != nil {
			return nil, "", err
		}
		if row.SigningCipher, err = s.encryptor.EncryptString(secret); err != nil {
			return nil, "", err
		}
		if err = store.insertConnection(ctx, row); err != nil {
			return nil, "", err
		}
		return row, secret, nil
	}
	return nil, "", ErrMailDomainZoneNotFound
}
func (s *service) UnbindCloudflare(ctx context.Context, org uint, id string) error {
	store, ok := s.repo.(cloudflareStore)
	if !ok {
		return ErrMailDomainsDisabled
	}
	return store.revokeConnection(ctx, org, id)
}
func (s *service) connectionConfig(ctx context.Context, d *domain.MailDomain) (config.MailboxConfig, error) {
	row, err := s.connection(ctx, d.OrganizationID, *d.CloudflareConnectionID)
	if err != nil {
		return config.MailboxConfig{}, err
	}
	if row == nil || row.ZoneID != d.ZoneID || (d.Name != row.ZoneName && !strings.HasSuffix(d.Name, "."+row.ZoneName)) {
		return config.MailboxConfig{}, ErrMailDomainsDisabled
	}
	token, err := s.encryptor.DecryptString(row.TokenCipher)
	return config.MailboxConfig{CloudflareAccountID: row.AccountID, CloudflareAPIToken: token, CloudflareEmailWorker: row.Worker}, err
}
func (s *service) domainClient(ctx context.Context, d *domain.MailDomain) (mailtransport.DomainProvider, string, error) {
	if d.CloudflareConnectionID == nil {
		return s.domainProvider, s.settings.EmailWorker, nil
	}
	cfg, err := s.connectionConfig(ctx, d)
	if err != nil {
		return nil, "", err
	}
	return mailtransport.NewCloudflareDomains(cfg), cfg.CloudflareEmailWorker, nil
}
func (s *service) ConnectCloudflareDomain(ctx context.Context, org, actor uint, id, name string, capability domain.MailDomainCapability) (*domain.MailDomain, error) {
	if err := s.requireDomains(); err != nil {
		return nil, err
	}
	name, err := s.normalizeDomainName(name)
	if err != nil {
		return nil, err
	}
	row, err := s.connection(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if row == nil || (name != row.ZoneName && !strings.HasSuffix(name, "."+row.ZoneName)) {
		return nil, ErrMailDomainZoneNotFound
	}
	if capability == "" {
		capability = domain.MailDomainSendReceive
	}
	if capability != domain.MailDomainSendOnly && capability != domain.MailDomainSendReceive {
		return nil, ErrMailDomainInvalid
	}
	d := &domain.MailDomain{CloudflareConnectionID: &row.ID, OrganizationID: org, Name: name, ZoneID: row.ZoneID, ZoneName: row.ZoneName, Capability: capability, Status: domain.MailDomainStatusPending, CreatedBy: actor}
	client, _, err := s.domainClient(ctx, d)
	if err != nil {
		return nil, err
	}
	records, err := client.ListRecords(ctx, d.ZoneID, name)
	if err != nil {
		return nil, ErrMailDomainProviderFailed
	}
	for _, r := range records {
		if receives(d) && strings.EqualFold(r.Type, "MX") && !isRoutingMX(r.Content) {
			return nil, ErrMailDomainTaken
		}
	}
	if err = s.domains.CreateDomain(ctx, d, domain.MaxMailDomainsPerOrganization); err != nil {
		return nil, err
	}
	return s.startSetUp(ctx, d)
}
func isRoutingMX(host string) bool {
	for _, value := range routingMXHosts {
		if strings.EqualFold(strings.TrimSuffix(host, "."), value) {
			return true
		}
	}
	return false
}

func (s *service) sendWithConnection(ctx context.Context, org uint, message mailtransport.Message) (mailtransport.Result, error) {
	if s.domains != nil {
		_, name, _ := strings.Cut(message.FromAddress, "@")
		d, err := s.domains.FindDomainByName(ctx, name)
		if err != nil && !errors.Is(err, ErrMailDomainNotFound) {
			return mailtransport.Result{}, err
		}
		if d != nil && d.CloudflareConnectionID != nil {
			if org != d.OrganizationID {
				return mailtransport.Result{}, ErrMailDomainsDisabled
			}
			cfg, err := s.connectionConfig(ctx, d)
			if err != nil {
				return mailtransport.Result{}, err
			}
			return mailtransport.NewCloudflare(cfg).Send(ctx, message)
		}
	}
	return s.transport.Send(ctx, message)
}

// Tenant administrators enable routing for each receiving domain once in their own
// account. This keeps Zone Settings Write out of the token used by the application.
func prepareReceiving(ctx context.Context, provider mailtransport.DomainProvider, d *domain.MailDomain) error {
	if d.CloudflareConnectionID == nil {
		return provider.EnableRouting(ctx, d.ZoneID, d.Name)
	}
	records, err := provider.ListRecords(ctx, d.ZoneID, d.Name)
	if err != nil {
		return err
	}
	for _, host := range routingMXHosts {
		check := compareRecord(domain.MailDomainRecord{Type: "MX", Content: host}, records)
		if check.Status != domain.MailDomainRecordOK {
			return errors.New("enable Email Routing for this exact domain in Cloudflare, then run setup again")
		}
	}
	return nil
}

func (s *service) CheckCloudflareAccess(ctx context.Context, org uint, id string) (mailtransport.CloudflareAccessChecks, error) {
	row, err := s.connection(ctx, org, id)
	if err != nil {
		return mailtransport.CloudflareAccessChecks{}, err
	}
	if row == nil {
		return mailtransport.CloudflareAccessChecks{}, ErrMailDomainNotFound
	}
	token, err := s.encryptor.DecryptString(row.TokenCipher)
	if err != nil {
		return mailtransport.CloudflareAccessChecks{}, err
	}
	client := mailtransport.NewCloudflareDomains(config.MailboxConfig{CloudflareAPIToken: token, CloudflareAccountID: row.AccountID})
	return client.CheckZoneAccess(ctx, row.ZoneID), nil
}
