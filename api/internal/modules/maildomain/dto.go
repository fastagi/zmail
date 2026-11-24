package maildomain

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailDomainRequest connects a domain.
type MailDomainRequest struct {
	Name       string `json:"name" binding:"required,max=253"`
	Capability string `json:"capability" binding:"omitempty,oneof=send_receive send_only"`
}

// MailDomainPreviewRequest names a domain to inspect before connecting.
type MailDomainPreviewRequest struct {
	Name string `json:"name" binding:"required,max=253"`
}

// MailDomainPreviewResponse is what connecting the domain would find.
type MailDomainPreviewResponse struct {
	Name                string   `json:"name"`
	Apex                string   `json:"apex"`
	ExistingMX          []string `json:"existing_mx"`
	SuggestedCapability string   `json:"suggested_capability"`
}

// MailZoneResponse is the public shape of a managed DNS zone.
type MailZoneResponse struct {
	ID                  uint       `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	NameServers         []string   `json:"name_servers"`
	OriginalNameServers []string   `json:"original_name_servers"`
	ActivatedAt         *time.Time `json:"activated_at"`
	LastCheckedAt       *time.Time `json:"last_checked_at"`
	CreatedAt           time.Time  `json:"created_at"`
}

// MailDomainRecordResponse is one DNS record a domain needs.
type MailDomainRecordResponse struct {
	Purpose  string `json:"purpose"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority int    `json:"priority,omitempty"`
	Status   string `json:"status"`
	Found    string `json:"found,omitempty"`
}

// MailDomainResponse is the public shape of a connected domain.
type MailDomainResponse struct {
	ID               uint                       `json:"id"`
	Name             string                     `json:"name"`
	ZoneName         string                     `json:"zone_name"`
	Capability       string                     `json:"capability"`
	Zone             *MailZoneResponse          `json:"zone"`
	Status           string                     `json:"status"`
	Records          []MailDomainRecordResponse `json:"records"`
	LastError        string                     `json:"last_error,omitempty"`
	LastErrorCode    string                     `json:"last_error_code,omitempty"`
	SettingUp        bool                       `json:"setting_up"`
	ZoneCheckLimited bool                       `json:"zone_check_limited"`
	Default          bool                       `json:"default"`
	MailboxCount     int                        `json:"mailbox_count"`
	CheckedAt        *time.Time                 `json:"checked_at"`
	VerifiedAt       *time.Time                 `json:"verified_at"`
	CreatedAt        time.Time                  `json:"created_at"`
}

func toMailZoneResponse(zone *domain.MailZone) *MailZoneResponse {
	if zone == nil {
		return nil
	}
	nameServers := zone.NameServers
	if nameServers == nil {
		nameServers = []string{}
	}
	originalNameServers := zone.OriginalNameServers
	if originalNameServers == nil {
		originalNameServers = []string{}
	}
	return &MailZoneResponse{
		ID:                  zone.ID,
		Name:                zone.Name,
		Status:              string(zone.Status),
		NameServers:         nameServers,
		OriginalNameServers: originalNameServers,
		ActivatedAt:         zone.ActivatedAt,
		LastCheckedAt:       zone.LastCheckedAt,
		CreatedAt:           zone.CreatedAt,
	}
}

func toMailDomainResponse(mailDomain *domain.MailDomain) MailDomainResponse {
	records := make([]MailDomainRecordResponse, 0, len(mailDomain.Records))
	for _, record := range mailDomain.Records {
		records = append(records, MailDomainRecordResponse{
			Purpose:  string(record.Purpose),
			Type:     record.Type,
			Name:     record.Name,
			Content:  record.Content,
			Priority: record.Priority,
			Status:   string(record.Status),
			Found:    record.Found,
		})
	}
	return MailDomainResponse{
		ID:               mailDomain.ID,
		Name:             mailDomain.Name,
		ZoneName:         mailDomain.ZoneName,
		Capability:       string(mailDomain.Capability),
		Zone:             toMailZoneResponse(mailDomain.Zone),
		Status:           string(mailDomain.Status),
		Records:          records,
		LastError:        mailDomain.LastError,
		LastErrorCode:    mailDomain.LastErrorCode,
		SettingUp:        mailDomain.SettingUp,
		ZoneCheckLimited: mailDomain.ZoneCheckLimited,
		Default:          mailDomain.Default,
		MailboxCount:     mailDomain.MailboxCount,
		CheckedAt:        mailDomain.CheckedAt,
		VerifiedAt:       mailDomain.VerifiedAt,
		CreatedAt:        mailDomain.CreatedAt,
	}
}
