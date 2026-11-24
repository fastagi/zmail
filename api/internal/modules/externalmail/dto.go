package externalmail

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ExternalMailConnectorResponse is one provider members can bind with.
type ExternalMailConnectorResponse struct {
	ID           string                           `json:"id"`
	AuthKind     string                           `json:"auth_kind"`
	Capabilities ExternalMailCapabilitiesResponse `json:"capabilities"`
}

// ExternalMailCapabilitiesResponse describes what a connector supports.
type ExternalMailCapabilitiesResponse struct {
	Read   bool `json:"read"`
	Send   bool `json:"send"`
	Tenant bool `json:"tenant"`
}

// ExternalMailPushStateResponse is a connection's push subscription state.
type ExternalMailPushStateResponse struct {
	Status         string     `json:"status"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastNotifiedAt *time.Time `json:"last_notified_at"`
	LastErrorCode  *string    `json:"last_error_code"`
}

// ExternalMailConnectionResponse is one bound external account. It never carries tokens.
type ExternalMailConnectionResponse struct {
	ID             uint                           `json:"id"`
	Provider       string                         `json:"provider"`
	AccountAddress string                         `json:"account_address"`
	MailboxID      uint                           `json:"mailbox_id"`
	Shared         bool                           `json:"shared"`
	TenantID       *uint                          `json:"tenant_id"`
	Status         string                         `json:"status"`
	LastSyncedAt   *time.Time                     `json:"last_synced_at"`
	LastErrorCode  *string                        `json:"last_error_code"`
	CreatedAt      time.Time                      `json:"created_at"`
	Push           *ExternalMailPushStateResponse `json:"push"`
}

// MailboxMemberRequest grants one member access to a shared connected mailbox.
type MailboxMemberRequest struct {
	UserID     uint   `json:"user_id" binding:"required,min=1"`
	Permission string `json:"permission" binding:"required,oneof=view send manage"`
}

type externalMailSharingRequest struct {
	DisplayName string                 `json:"display_name" binding:"max=255"`
	Members     []MailboxMemberRequest `json:"members" binding:"omitempty,dive"`
}

func (r *externalMailSharingRequest) input() *domain.ExternalMailSharing {
	if r == nil {
		return nil
	}
	sharing := &domain.ExternalMailSharing{
		DisplayName: r.DisplayName,
		Members:     make([]domain.ExternalMailSharingMember, 0, len(r.Members)),
	}
	for _, member := range r.Members {
		sharing.Members = append(sharing.Members, domain.ExternalMailSharingMember{
			UserID:     member.UserID,
			Permission: domain.MailboxPermission(member.Permission),
		})
	}
	return sharing
}

type startExternalMailAuthorizationRequest struct {
	Provider     string                      `json:"provider" binding:"omitempty,max=16"`
	ConnectionID *uint                       `json:"connection_id" binding:"omitempty,min=1"`
	BackfillDays int                         `json:"backfill_days"`
	Sharing      *externalMailSharingRequest `json:"sharing"`
}

type completeExternalMailAuthorizationRequest struct {
	State string `json:"state" binding:"required,max=128"`
	Code  string `json:"code" binding:"required,max=4096"`
}

// ExternalMailAuthorizationResponse is where to send the browser for OAuth.
type ExternalMailAuthorizationResponse struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func toExternalMailPushState(subscription *domain.ExternalMailSubscription, status domain.ExternalMailSubscriptionStatus) *ExternalMailPushStateResponse {
	if subscription == nil {
		return nil
	}
	if status == "" {
		status = subscription.Status
	}
	state := &ExternalMailPushStateResponse{
		Status:        string(status),
		LastErrorCode: optionalText(subscription.LastErrorCode),
	}
	if subscription.ExpiresAt != nil && subscription.Status == domain.ExternalMailSubscriptionActive {
		ts := subscription.ExpiresAt.UTC()
		state.ExpiresAt = &ts
	}
	if subscription.LastNotifiedAt != nil {
		ts := subscription.LastNotifiedAt.UTC()
		state.LastNotifiedAt = &ts
	}
	return state
}

func toExternalMailConnectionResponses(connections []*domain.ExternalMailConnection) []ExternalMailConnectionResponse {
	responses := make([]ExternalMailConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		item := ExternalMailConnectionResponse{
			ID:             connection.ID,
			Provider:       string(connection.Provider),
			AccountAddress: connection.AccountAddress,
			MailboxID:      connection.MailboxID,
			Shared:         connection.Shared,
			TenantID:       connection.TenantID,
			Status:         string(connection.Status),
			LastErrorCode:  optionalText(connection.LastErrorCode),
			CreatedAt:      connection.CreatedAt.UTC(),
		}
		if connection.LastSyncedAt != nil {
			ts := connection.LastSyncedAt.UTC()
			item.LastSyncedAt = &ts
		}
		item.Push = toExternalMailPushState(connection.Push, connection.PushStatus)
		responses = append(responses, item)
	}
	return responses
}
