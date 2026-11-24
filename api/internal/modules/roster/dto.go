package roster

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// MailContactRequest creates or replaces a contact. Email is used only when creating.
type MailContactRequest struct {
	Email      string            `json:"email" binding:"max=320"`
	FirstName  string            `json:"first_name" binding:"max=400"`
	LastName   string            `json:"last_name" binding:"max=400"`
	Properties map[string]string `json:"properties" binding:"omitempty,max=50"`
	Status     string            `json:"status" binding:"omitempty,oneof=subscribed unsubscribed suppressed"`
	ListIDs    []uint            `json:"list_ids" binding:"omitempty,max=100"`
}

func (r MailContactRequest) input() ContactInput {
	return ContactInput{
		Email:      r.Email,
		FirstName:  r.FirstName,
		LastName:   r.LastName,
		Properties: r.Properties,
		Status:     domain.MailContactStatus(r.Status),
		ListIDs:    r.ListIDs,
	}
}

// MailContactImportRequest imports CSV text with a header row.
type MailContactImportRequest struct {
	CSV    string `json:"csv" binding:"required"`
	ListID uint   `json:"list_id"`
}

// MailContactListRequest creates or renames a list.
type MailContactListRequest struct {
	Name string `json:"name" binding:"required,max=400"`
}

// MailContactResponse is the public shape of a contact.
type MailContactResponse struct {
	ID           uint              `json:"id"`
	Email        string            `json:"email"`
	FirstName    string            `json:"first_name"`
	LastName     string            `json:"last_name"`
	Properties   map[string]string `json:"properties"`
	Status       string            `json:"status"`
	StatusReason string            `json:"status_reason"`
	ListIDs      []uint            `json:"list_ids"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// MailContactListResponse is the public shape of a list.
type MailContactListResponse struct {
	ID           uint      `json:"id"`
	Name         string    `json:"name"`
	ContactCount int       `json:"contact_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// MailContactImportSkipResponse is one row an import did not store.
type MailContactImportSkipResponse struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// MailContactImportResponse reports what an import did.
type MailContactImportResponse struct {
	Rows           int                             `json:"rows"`
	Created        int                             `json:"created"`
	Updated        int                             `json:"updated"`
	SkippedCount   int                             `json:"skipped_count"`
	Skipped        []MailContactImportSkipResponse `json:"skipped"`
	IgnoredColumns []string                        `json:"ignored_columns"`
}

func toMailContactResponse(contact *domain.MailContact) MailContactResponse {
	properties := contact.Properties
	if properties == nil {
		properties = map[string]string{}
	}
	listIDs := contact.ListIDs
	if listIDs == nil {
		listIDs = []uint{}
	}
	return MailContactResponse{
		ID:           contact.ID,
		Email:        contact.Email,
		FirstName:    contact.FirstName,
		LastName:     contact.LastName,
		Properties:   properties,
		Status:       string(contact.Status),
		StatusReason: contact.StatusReason,
		ListIDs:      listIDs,
		CreatedAt:    contact.CreatedAt,
		UpdatedAt:    contact.UpdatedAt,
	}
}

func toMailContactListResponse(list *domain.MailContactList) MailContactListResponse {
	return MailContactListResponse{
		ID:           list.ID,
		Name:         list.Name,
		ContactCount: list.ContactCount,
		CreatedAt:    list.CreatedAt,
	}
}
