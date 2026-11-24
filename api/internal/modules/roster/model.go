package roster

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// ImportBatch bounds how many rows one batched SQL write inserts.
const ImportBatch = 500

// MailContactPO is the persistent object for one mail contact.
type MailContactPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:idx_mail_contacts_email,priority:1;index:idx_mail_contacts_listing,priority:1"`
	Email          string `gorm:"size:320;not null;uniqueIndex:idx_mail_contacts_email,priority:2"`
	FirstName      string `gorm:"size:100;not null;default:''"`
	LastName       string `gorm:"size:100;not null;default:''"`
	Properties     string `gorm:"type:jsonb;not null;default:'{}'"`
	Status         string `gorm:"size:16;not null;default:'subscribed'"`
	StatusReason   string `gorm:"size:64;not null;default:''"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailContactPO) TableName() string {
	return "mail_contacts"
}

func (po *MailContactPO) toDomain() *domain.MailContact {
	return &domain.MailContact{
		ID:             po.ID,
		OrganizationID: po.OrganizationID,
		Email:          po.Email,
		FirstName:      po.FirstName,
		LastName:       po.LastName,
		Properties:     decodeProperties(po.Properties),
		Status:         domain.MailContactStatus(po.Status),
		StatusReason:   po.StatusReason,
		ListIDs:        []uint{},
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
	}
}

// MailContactListPO is the persistent object for one contact list.
type MailContactListPO struct {
	ID             uint   `gorm:"primaryKey"`
	OrganizationID uint   `gorm:"not null;uniqueIndex:idx_mail_contact_lists_name,priority:1"`
	Name           string `gorm:"size:100;not null;uniqueIndex:idx_mail_contact_lists_name,priority:2"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (MailContactListPO) TableName() string {
	return "mail_contact_lists"
}

// MailContactListMemberPO associates one contact with one list.
type MailContactListMemberPO struct {
	ListID    uint `gorm:"primaryKey;autoIncrement:false"`
	ContactID uint `gorm:"primaryKey;autoIncrement:false;index:idx_mail_contact_list_members_contact"`
	CreatedAt time.Time
}

func (MailContactListMemberPO) TableName() string {
	return "mail_contact_list_members"
}

func decodeProperties(encoded string) map[string]string {
	properties := map[string]string{}
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &properties); err != nil || properties == nil {
			properties = map[string]string{}
		}
	}
	return properties
}

func encodeProperties(properties map[string]string) string {
	if properties == nil {
		properties = map[string]string{}
	}
	encoded, err := json.Marshal(properties)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var stateError interface{ SQLState() string }
	return errors.As(err, &stateError) && stateError.SQLState() == "23505"
}

func cleanText(value string) string {
	value = strings.ToValidUTF8(value, "\ufffd")
	return strings.ReplaceAll(value, "\x00", "")
}

func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
