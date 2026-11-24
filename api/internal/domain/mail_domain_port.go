package domain

import (
	"context"
	"errors"
)

// Mail domain and zone sentinel errors shared across the maildomain and mailbox modules.
var (
	ErrMailDomainNotFound       = errors.New("mail domain not found")
	ErrMailDomainInvalid        = errors.New("mail domain name is invalid")
	ErrMailDomainTaken          = errors.New("mail domain is already connected")
	ErrMailDomainLimitReached   = errors.New("mail domain limit reached")
	ErrMailDomainZoneNotFound   = errors.New("no manageable DNS zone contains the mail domain")
	ErrMailDomainInUse          = errors.New("mail domain still has mailboxes")
	ErrMailDomainNotVerified    = errors.New("mail domain is not verified")
	ErrMailDomainsDisabled      = errors.New("organization mail domains are not enabled")
	ErrMailDomainProviderFailed = errors.New("the DNS provider rejected the request")
	ErrMailZoneNotFound         = errors.New("mail zone not found")
	ErrMailZoneLimitReached     = errors.New("mail zone limit reached")
	ErrMailZoneInUse            = errors.New("mail zone still has mail domains")
	ErrMailZoneStillDelegated   = errors.New("mail zone is still delegated to its assigned name servers")
	ErrMailZonePending          = errors.New("mail zone is not active yet")
	ErrMailZoneCheckLimited     = errors.New("mail zone was checked too recently")
)

// MailDomainPreviewView describes a domain's apex and public MX records prior to connecting.
type MailDomainPreviewView struct {
	Name                string
	Apex                string
	ExistingMX          []string
	SuggestedCapability MailDomainCapability
}

// MailDomainPort is the hexagonal port exposed to the top-level maildomain module.
type MailDomainPort interface {
	ListMailDomainsPort(ctx context.Context, organizationID uint) ([]*MailDomain, error)
	GetMailDomainPort(ctx context.Context, organizationID, id uint) (*MailDomain, error)
	ConnectMailDomainPort(ctx context.Context, organizationID, userID uint, name string, capability MailDomainCapability) (*MailDomain, error)
	SetUpMailDomainPort(ctx context.Context, organizationID, id uint) (*MailDomain, error)
	CheckMailDomainPort(ctx context.Context, organizationID, id uint) (*MailDomain, error)
	EnableMailDomainReceivingPort(ctx context.Context, organizationID, id uint) (*MailDomain, error)
	SetDefaultMailDomainPort(ctx context.Context, organizationID, id uint) (*MailDomain, error)
	DeleteMailDomainPort(ctx context.Context, organizationID, id uint) error
	PreviewMailDomainPort(ctx context.Context, name string) (MailDomainPreviewView, error)
	ListMailZonesPort(ctx context.Context, organizationID uint) ([]*MailZone, error)
	GetMailZonePort(ctx context.Context, organizationID, id uint) (*MailZone, error)
	DeleteMailZonePort(ctx context.Context, organizationID, id uint) error
}
