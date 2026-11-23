package apikey

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
)

// APIKeyPO is the persistent object for API keys.
type APIKeyPO struct {
	ID         uint `gorm:"primaryKey"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  gorm.DeletedAt `gorm:"index"`
	UserID     uint           `gorm:"not null;index"`
	Name       string         `gorm:"size:100;not null"`
	KeyPrefix  string         `gorm:"size:32;not null;index"`
	KeyHash    string         `gorm:"size:64;not null;uniqueIndex"`
	Scopes     string         `gorm:"type:text"`
	Mode       string         `gorm:"size:8;not null;default:live"`
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	// OrganizationID binds the key to one organization; NULL leaves it unrestricted.
	OrganizationID *uint
	// SendingDomains is a JSON array of the domains the key may send from; NULL allows every one.
	SendingDomains *string `gorm:"type:text"`
}

// TableName specifies the database table name.
func (APIKeyPO) TableName() string {
	return "api_keys"
}

func (po *APIKeyPO) toDomain() (*domain.APIKey, error) {
	if po == nil {
		return nil, nil
	}
	scopes, err := decodeScopes(po.Scopes)
	if err != nil {
		return nil, fmt.Errorf("decode api key scopes: %w", err)
	}

	sendingDomains, err := decodeSendingDomains(po.SendingDomains)
	if err != nil {
		return nil, fmt.Errorf("decode api key sending domains: %w", err)
	}

	return &domain.APIKey{
		ID:             po.ID,
		UserID:         po.UserID,
		Name:           po.Name,
		KeyPrefix:      po.KeyPrefix,
		KeyHash:        po.KeyHash,
		Scopes:         scopes,
		Mode:           storedMode(po.Mode),
		LastUsedAt:     po.LastUsedAt,
		ExpiresAt:      po.ExpiresAt,
		RevokedAt:      po.RevokedAt,
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
		OrganizationID: po.OrganizationID,
		SendingDomains: sendingDomains,
	}, nil
}

func newAPIKeyPO(key *domain.APIKey) (*APIKeyPO, error) {
	if key == nil {
		return nil, nil
	}
	encodedScopes, err := encodeScopes(key.Scopes)
	if err != nil {
		return nil, fmt.Errorf("encode api key scopes: %w", err)
	}

	encodedDomains, err := encodeSendingDomains(key.SendingDomains)
	if err != nil {
		return nil, fmt.Errorf("encode api key sending domains: %w", err)
	}

	return &APIKeyPO{
		ID:             key.ID,
		CreatedAt:      key.CreatedAt,
		UpdatedAt:      key.UpdatedAt,
		UserID:         key.UserID,
		Name:           key.Name,
		KeyPrefix:      key.KeyPrefix,
		KeyHash:        key.KeyHash,
		Scopes:         encodedScopes,
		Mode:           string(storedMode(string(key.Mode))),
		LastUsedAt:     key.LastUsedAt,
		ExpiresAt:      key.ExpiresAt,
		RevokedAt:      key.RevokedAt,
		OrganizationID: key.OrganizationID,
		SendingDomains: encodedDomains,
	}, nil
}

// storedMode reads a persisted mode; anything but test is live, matching the column default.
func storedMode(value string) domain.APIKeyMode {
	if domain.APIKeyMode(value) == domain.APIKeyModeTest {
		return domain.APIKeyModeTest
	}
	return domain.APIKeyModeLive
}

func toDomainList(items []*APIKeyPO) ([]*domain.APIKey, error) {
	result := make([]*domain.APIKey, len(items))
	for i, item := range items {
		key, err := item.toDomain()
		if err != nil {
			return nil, err
		}
		result[i] = key
	}
	return result, nil
}

func decodeScopes(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}

	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "[") {
		var scopes []string
		if err := json.Unmarshal([]byte(trimmed), &scopes); err != nil {
			return nil, err
		}
		if scopes == nil {
			return []string{}, nil
		}
		return scopes, nil
	}

	// Compatibility for rows written before scopes became JSON encoded.
	parts := strings.Split(trimmed, ",")
	scopes := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			scopes = append(scopes, part)
		}
	}
	return scopes, nil
}

func encodeScopes(scopes []string) (string, error) {
	if scopes == nil {
		scopes = []string{}
	}
	encoded, err := json.Marshal(scopes)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// decodeSendingDomains reads the stored domain list; NULL means every domain.
func decodeSendingDomains(value *string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	domains := []string{}
	if err := json.Unmarshal([]byte(*value), &domains); err != nil {
		return nil, err
	}
	if domains == nil {
		domains = []string{}
	}
	return domains, nil
}

// encodeSendingDomains stores nil as NULL so an unrestricted key never reads back as restricted.
func encodeSendingDomains(domains []string) (*string, error) {
	if domains == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(domains)
	if err != nil {
		return nil, err
	}
	value := string(encoded)
	return &value, nil
}
