package domain

import (
	"context"
	"time"
)

// MaxMailDomainsPerOrganization bounds how many domains one organization connects.
const MaxMailDomainsPerOrganization = 10

// MailDomainStatus tells whether every record a connected domain needs is in place.
type MailDomainStatus string

const (
	MailDomainStatusPending  MailDomainStatus = "pending"
	MailDomainStatusVerified MailDomainStatus = "verified"
)

// MailDomainCapability tells whether a connected domain receives mail through Zmail or only sends.
type MailDomainCapability string

const (
	MailDomainSendReceive MailDomainCapability = "send_receive"
	// MailDomainSendOnly leaves the domain's MX with another mail service.
	MailDomainSendOnly MailDomainCapability = "send_only"
)

// MailDomainRecordPurpose names what a required record is for.
type MailDomainRecordPurpose string

const (
	MailDomainRecordReceiving MailDomainRecordPurpose = "receiving"
	MailDomainRecordSending   MailDomainRecordPurpose = "sending"
	MailDomainRecordDMARC     MailDomainRecordPurpose = "dmarc"
)

// MailDomainRecordStatus is what the last check found for one required record.
type MailDomainRecordStatus string

const (
	MailDomainRecordOK      MailDomainRecordStatus = "ok"
	MailDomainRecordMissing MailDomainRecordStatus = "missing"
	// MailDomainRecordConflict means another record of the same kind holds the name, such as an
	// SPF record or MX of a different mail service. Zmail never replaces it.
	MailDomainRecordConflict MailDomainRecordStatus = "conflict"
)

// MailDomainRecord is one DNS record a connected domain needs, with what the last check found.
type MailDomainRecord struct {
	Purpose  MailDomainRecordPurpose `json:"purpose"`
	Type     string                  `json:"type"`
	Name     string                  `json:"name"`
	Content  string                  `json:"content"`
	Priority int                     `json:"priority,omitempty"`
	Status   MailDomainRecordStatus  `json:"status"`
	// Found is the conflicting value, when Status is conflict.
	Found string `json:"found,omitempty"`
}

// MailDomain is a domain an organization connected, on which it may create mailboxes once
// every required record is in place.
type MailDomain struct {
	ID             uint
	OrganizationID uint
	Name           string
	ZoneID         string
	ZoneName       string
	// MailZoneID is the managed zone the domain lives in; nil for a zone the operator manages.
	MailZoneID *uint
	Capability MailDomainCapability
	// Zone is the managed zone MailZoneID names, filled on reads; it is not stored.
	Zone *MailZone
	// SendingTag identifies the domain's Email Sending configuration at the provider.
	SendingTag string
	Status     MailDomainStatus
	Records    []MailDomainRecord
	// LastError is the provider's explanation of the last failed setup or check.
	LastError string
	// SettingUp is true while a setup runs in the background; it is not stored.
	SettingUp  bool
	CheckedAt  *time.Time
	VerifiedAt *time.Time
	CreatedBy  uint
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MailDomainRepository defines persistence for connected domains.
type MailDomainRepository interface {
	// CreateDomain stores a domain unless the organization already has limit domains or the
	// name is taken anywhere on the instance.
	CreateDomain(ctx context.Context, domain *MailDomain, limit int) error
	ListDomains(ctx context.Context, organizationID uint) ([]*MailDomain, error)
	FindDomain(ctx context.Context, organizationID, id uint) (*MailDomain, error)
	FindDomainByName(ctx context.Context, name string) (*MailDomain, error)
	UpdateDomain(ctx context.Context, domain *MailDomain) error
	// DeleteDomain removes a domain that no live mailbox uses.
	DeleteDomain(ctx context.Context, organizationID, id uint) error
	SetMailboxRoutingRule(ctx context.Context, mailboxID uint, ruleID string) error
}
