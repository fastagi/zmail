package roster

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

const (
	maxContactNameLength     = 100
	maxContactPropertyLength = 500
	maxContactQueryLength    = 200
	maxPropertyNameLength    = 40
	// MaxContactImportBytes bounds the CSV text of one import.
	MaxContactImportBytes = 5 * 1024 * 1024
	// MaxContactImportRows bounds the data rows of one import.
	MaxContactImportRows   = 10_000
	maxContactImportFields = 60
	// maxReportedSkips bounds how many skipped rows an import result lists one by one.
	maxReportedSkips = 100
	// statusReasonManual marks a contact a member unsubscribed by hand.
	statusReasonManual = "manual"
	// RFC 5321 section 4.5.3.1 limits.
	maxAddressLength   = 254
	maxLocalPartLength = 64
	maxDomainLabel     = 63
)

// Skip reasons an import reports for a row it did not store.
const (
	SkipInvalidEmail      = "invalid_email"
	SkipDuplicate         = "duplicate"
	SkipInvalidName       = "invalid_name"
	SkipInvalidProperty   = "invalid_property"
	SkipTooManyProperties = "too_many_properties"
)

// reservedContactFields are columns with a fixed meaning; they are not custom properties.
var reservedContactFields = []string{"email", "first_name", "last_name"}

// ContactInput is the editable content of a contact. Email is used only when creating.
type ContactInput struct {
	Email      string
	FirstName  string
	LastName   string
	Properties map[string]string
	// Status is subscribed or unsubscribed; empty keeps the current status, or subscribes a new contact.
	Status  domain.MailContactStatus
	ListIDs []uint
}

// ContactImportSkip is one row an import did not store. Line counts from 1 and includes the header.
type ContactImportSkip struct {
	Line   int
	Reason string
}

// ContactImportResult says what an import did.
type ContactImportResult struct {
	Rows    int
	Created int
	Updated int
	// Skipped lists at most 100 skipped rows; SkippedCount counts them all.
	Skipped        []ContactImportSkip
	SkippedCount   int
	IgnoredColumns []string
}

// Service owns mail contacts, contact lists, and CSV contact imports.
type Service interface {
	ListContacts(ctx context.Context, organizationID uint, filter domain.MailContactFilter) ([]*domain.MailContact, int64, error)
	GetContact(ctx context.Context, organizationID, id uint) (*domain.MailContact, error)
	CreateContact(ctx context.Context, organizationID uint, input ContactInput) (*domain.MailContact, error)
	UpdateContact(ctx context.Context, organizationID, id uint, input ContactInput) (*domain.MailContact, error)
	DeleteContact(ctx context.Context, organizationID, id uint) error
	ImportContacts(ctx context.Context, organizationID uint, text string, listID uint) (*ContactImportResult, error)
	ListContactLists(ctx context.Context, organizationID uint) ([]*domain.MailContactList, error)
	CreateContactList(ctx context.Context, organizationID uint, name string) (*domain.MailContactList, error)
	RenameContactList(ctx context.Context, organizationID, id uint, name string) (*domain.MailContactList, error)
	DeleteContactList(ctx context.Context, organizationID, id uint) error
}

type service struct {
	contacts domain.MailContactRepository
}

// NewService constructs the roster Service backed by a domain.MailContactRepository port.
func NewService(contacts domain.MailContactRepository) Service {
	return &service{contacts: contacts}
}

func (s *service) ListContacts(ctx context.Context, organizationID uint, filter domain.MailContactFilter) ([]*domain.MailContact, int64, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	if utf8.RuneCountInString(filter.Query) > maxContactQueryLength || (filter.Status != "" && !filter.Status.Valid()) {
		return nil, 0, domain.ErrInvalidInput
	}
	return s.contacts.ListContacts(ctx, organizationID, filter)
}

func (s *service) GetContact(ctx context.Context, organizationID, id uint) (*domain.MailContact, error) {
	return s.contacts.FindContact(ctx, organizationID, id)
}

func (s *service) CreateContact(ctx context.Context, organizationID uint, input ContactInput) (*domain.MailContact, error) {
	email, ok := normalizeRecipient(input.Email)
	if !ok {
		return nil, fmt.Errorf("%w: email", domain.ErrMailContactInvalid)
	}
	contact, err := buildContact(input)
	if err != nil {
		return nil, err
	}
	contact.OrganizationID, contact.Email = organizationID, email
	if contact.Status == "" {
		contact.Status = domain.MailContactSubscribed
	}
	contact.StatusReason = statusReason(contact.Status, "")
	if err := s.contacts.CreateContact(ctx, contact, domain.MaxMailContactsPerOrganization); err != nil {
		return nil, err
	}
	return contact, nil
}

// UpdateContact replaces the names, properties, status, and lists of a contact. The address
// never changes. A suppressed contact stays suppressed unless the request names another status.
func (s *service) UpdateContact(ctx context.Context, organizationID, id uint, input ContactInput) (*domain.MailContact, error) {
	current, err := s.contacts.FindContact(ctx, organizationID, id)
	if err != nil {
		return nil, err
	}
	contact, err := buildContact(input)
	if err != nil {
		return nil, err
	}
	contact.ID, contact.OrganizationID, contact.Email = id, organizationID, current.Email
	if contact.Status == "" || contact.Status == current.Status {
		contact.Status, contact.StatusReason = current.Status, current.StatusReason
	} else {
		contact.StatusReason = statusReason(contact.Status, current.StatusReason)
	}
	if err := s.contacts.UpdateContact(ctx, contact); err != nil {
		return nil, err
	}
	return contact, nil
}

func (s *service) DeleteContact(ctx context.Context, organizationID, id uint) error {
	return s.contacts.DeleteContact(ctx, organizationID, id)
}

func (s *service) ImportContacts(ctx context.Context, organizationID uint, text string, listID uint) (*ContactImportResult, error) {
	if len(text) > MaxContactImportBytes || !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: the file must be UTF-8 text of at most 5 MiB", domain.ErrMailContactImportInvalid)
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: a header row is required", domain.ErrMailContactImportInvalid)
	}
	columns, ignored, err := importColumns(header)
	if err != nil {
		return nil, err
	}

	result := &ContactImportResult{Skipped: []ContactImportSkip{}, IgnoredColumns: ignored}
	skip := func(line int, reason string) {
		result.SkippedCount++
		if len(result.Skipped) < maxReportedSkips {
			result.Skipped = append(result.Skipped, ContactImportSkip{Line: line, Reason: reason})
		}
	}
	seen := map[string]struct{}{}
	var rows []domain.MailContactImportRow
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			var parseErr *csv.ParseError
			if errors.As(readErr, &parseErr) {
				return nil, fmt.Errorf("%w: line %d is not valid CSV", domain.ErrMailContactImportInvalid, parseErr.Line)
			}
			return nil, fmt.Errorf("%w: the file is not valid CSV", domain.ErrMailContactImportInvalid)
		}
		if blankRecord(record) {
			continue
		}
		line, _ := reader.FieldPos(0)
		result.Rows++
		if result.Rows > MaxContactImportRows {
			return nil, fmt.Errorf("%w: at most %d rows", domain.ErrMailContactImportInvalid, MaxContactImportRows)
		}
		row, reason := importRow(columns, record)
		if reason == "" {
			if _, duplicate := seen[row.Email]; duplicate {
				reason = SkipDuplicate
			}
		}
		if reason != "" {
			skip(line, reason)
			continue
		}
		seen[row.Email] = struct{}{}
		rows = append(rows, row)
	}

	if len(rows) > 0 || listID != 0 {
		counts, err := s.contacts.ImportContacts(ctx, organizationID, rows, listID, domain.MaxMailContactsPerOrganization)
		if err != nil {
			return nil, err
		}
		result.Created, result.Updated = counts.Created, counts.Updated
	}
	return result, nil
}

func (s *service) ListContactLists(ctx context.Context, organizationID uint) ([]*domain.MailContactList, error) {
	return s.contacts.ListContactLists(ctx, organizationID)
}

func (s *service) CreateContactList(ctx context.Context, organizationID uint, name string) (*domain.MailContactList, error) {
	cleaned, ok := cleanListName(name)
	if !ok {
		return nil, fmt.Errorf("%w: name", domain.ErrMailContactInvalid)
	}
	list := &domain.MailContactList{OrganizationID: organizationID, Name: cleaned}
	if err := s.contacts.CreateContactList(ctx, list, domain.MaxMailContactListsPerOrganization); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *service) RenameContactList(ctx context.Context, organizationID, id uint, name string) (*domain.MailContactList, error) {
	cleaned, ok := cleanListName(name)
	if !ok {
		return nil, fmt.Errorf("%w: name", domain.ErrMailContactInvalid)
	}
	return s.contacts.RenameContactList(ctx, organizationID, id, cleaned)
}

func (s *service) DeleteContactList(ctx context.Context, organizationID, id uint) error {
	return s.contacts.DeleteContactList(ctx, organizationID, id)
}

func statusReason(status domain.MailContactStatus, previous string) string {
	switch status {
	case domain.MailContactUnsubscribed:
		return statusReasonManual
	case domain.MailContactSuppressed:
		return previous
	default:
		return ""
	}
}

func buildContact(input ContactInput) (*domain.MailContact, error) {
	firstName, ok := cleanContactName(input.FirstName)
	if !ok {
		return nil, fmt.Errorf("%w: first_name", domain.ErrMailContactInvalid)
	}
	lastName, ok := cleanContactName(input.LastName)
	if !ok {
		return nil, fmt.Errorf("%w: last_name", domain.ErrMailContactInvalid)
	}
	properties, reason := cleanContactProperties(input.Properties)
	if reason != "" {
		return nil, fmt.Errorf("%w: properties", domain.ErrMailContactInvalid)
	}
	switch input.Status {
	case "", domain.MailContactSubscribed, domain.MailContactUnsubscribed:
	default:
		return nil, fmt.Errorf("%w: status", domain.ErrMailContactInvalid)
	}
	listIDs, ok := cleanListIDs(input.ListIDs)
	if !ok {
		return nil, fmt.Errorf("%w: list_ids", domain.ErrMailContactInvalid)
	}
	return &domain.MailContact{
		FirstName:  firstName,
		LastName:   lastName,
		Properties: properties,
		Status:     input.Status,
		ListIDs:    listIDs,
	}, nil
}

func cleanContactName(raw string) (string, bool) {
	name := strings.Join(strings.Fields(cleanText(raw)), " ")
	if utf8.RuneCountInString(name) > maxContactNameLength || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", false
	}
	return name, true
}

func cleanContactProperties(raw map[string]string) (map[string]string, string) {
	properties := make(map[string]string, len(raw))
	for name, value := range raw {
		if !validPropertyName(name) || slices.Contains(reservedContactFields, name) {
			return nil, SkipInvalidProperty
		}
		value = strings.TrimSpace(cleanText(value))
		if utf8.RuneCountInString(value) > maxContactPropertyLength {
			return nil, SkipInvalidProperty
		}
		if value != "" {
			properties[name] = value
		}
	}
	if len(properties) > domain.MaxMailContactProperties {
		return nil, SkipTooManyProperties
	}
	return properties, ""
}

func cleanListIDs(raw []uint) ([]uint, bool) {
	if len(raw) > domain.MaxMailContactListsPerOrganization {
		return nil, false
	}
	ids := make([]uint, 0, len(raw))
	for _, id := range raw {
		if id == 0 {
			return nil, false
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, true
}

type importColumn struct {
	field    string
	property bool
}

func importColumns(header []string) ([]importColumn, []string, error) {
	if len(header) > maxContactImportFields {
		return nil, nil, fmt.Errorf("%w: at most %d columns", domain.ErrMailContactImportInvalid, maxContactImportFields)
	}
	columns := make([]importColumn, len(header))
	ignored := []string{}
	seen := map[string]struct{}{}
	hasEmail := false
	for index, raw := range header {
		name := strings.ToLower(strings.TrimSpace(raw))
		name = strings.NewReplacer(" ", "_", "-", "_").Replace(name)
		if name == "e_mail" || name == "email_address" {
			name = "email"
		}
		if _, duplicate := seen[name]; duplicate && name != "" {
			return nil, nil, fmt.Errorf("%w: column %q appears twice", domain.ErrMailContactImportInvalid, raw)
		}
		seen[name] = struct{}{}
		switch {
		case name == "email":
			hasEmail = true
			columns[index] = importColumn{field: name}
		case name == "first_name" || name == "last_name":
			columns[index] = importColumn{field: name}
		case validPropertyName(name):
			columns[index] = importColumn{field: name, property: true}
		default:
			if trimmed := strings.TrimSpace(raw); trimmed != "" {
				ignored = append(ignored, truncateText(cleanText(trimmed), 100))
			}
		}
	}
	if !hasEmail {
		return nil, nil, fmt.Errorf("%w: an email column is required", domain.ErrMailContactImportInvalid)
	}
	return columns, ignored, nil
}

func importRow(columns []importColumn, record []string) (domain.MailContactImportRow, string) {
	row := domain.MailContactImportRow{}
	properties := map[string]string{}
	for index, value := range record {
		if index >= len(columns) || columns[index].field == "" {
			continue
		}
		column := columns[index]
		switch {
		case column.field == "email":
			row.Email = value
		case column.field == "first_name":
			row.FirstName = value
		case column.field == "last_name":
			row.LastName = value
		case column.property:
			properties[column.field] = value
		}
	}
	email, ok := normalizeRecipient(row.Email)
	if !ok {
		return row, SkipInvalidEmail
	}
	row.Email = email
	if row.FirstName, ok = cleanContactName(row.FirstName); !ok {
		return row, SkipInvalidName
	}
	if row.LastName, ok = cleanContactName(row.LastName); !ok {
		return row, SkipInvalidName
	}
	cleaned, reason := cleanContactProperties(properties)
	if reason != "" {
		return row, reason
	}
	row.Properties = cleaned
	return row, ""
}

func blankRecord(record []string) bool {
	for _, value := range record {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func cleanListName(raw string) (string, bool) {
	name := strings.Join(strings.Fields(cleanText(raw)), " ")
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", false
	}
	return name, true
}

func validPropertyName(name string) bool {
	if name == "" || len(name) > maxPropertyNameLength {
		return false
	}
	for index, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z':
		case index > 0 && (char >= '0' && char <= '9' || char == '_'):
		default:
			return false
		}
	}
	return true
}

func normalizeRecipient(raw string) (string, bool) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || len(candidate) > 320 || strings.ContainsAny(candidate, "<>\r\n\"") {
		return "", false
	}
	parsed, err := mail.ParseAddress(candidate)
	if err != nil || parsed.Name != "" {
		return "", false
	}
	at := strings.LastIndex(parsed.Address, "@")
	if at <= 0 || at == len(parsed.Address)-1 || !strings.Contains(parsed.Address[at+1:], ".") {
		return "", false
	}
	if len(parsed.Address) > maxAddressLength || at > maxLocalPartLength {
		return "", false
	}
	for _, label := range strings.Split(parsed.Address[at+1:], ".") {
		if label == "" || len(label) > maxDomainLabel {
			return "", false
		}
	}
	return strings.ToLower(parsed.Address), true
}
