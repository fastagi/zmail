package mailbox

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
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
	// MaxContactImportBytes bounds the CSV text of one import.
	MaxContactImportBytes = 5 * 1024 * 1024
	// MaxContactImportRows bounds the data rows of one import.
	MaxContactImportRows   = 10_000
	maxContactImportFields = 60
	// maxReportedSkips bounds how many skipped rows an import result lists one by one.
	maxReportedSkips = 100
	// statusReasonManual marks a contact a member unsubscribed by hand.
	statusReasonManual = "manual"
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
		return nil, fmt.Errorf("%w: email", ErrMailContactInvalid)
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

// buildContact validates the editable fields. Members may set subscribed or unsubscribed;
// suppressed is set only by Zmail.
func buildContact(input ContactInput) (*domain.MailContact, error) {
	firstName, ok := cleanContactName(input.FirstName)
	if !ok {
		return nil, fmt.Errorf("%w: first_name", ErrMailContactInvalid)
	}
	lastName, ok := cleanContactName(input.LastName)
	if !ok {
		return nil, fmt.Errorf("%w: last_name", ErrMailContactInvalid)
	}
	properties, reason := cleanContactProperties(input.Properties)
	if reason != "" {
		return nil, fmt.Errorf("%w: properties", ErrMailContactInvalid)
	}
	switch input.Status {
	case "", domain.MailContactSubscribed, domain.MailContactUnsubscribed:
	default:
		return nil, fmt.Errorf("%w: status", ErrMailContactInvalid)
	}
	listIDs, ok := cleanListIDs(input.ListIDs)
	if !ok {
		return nil, fmt.Errorf("%w: list_ids", ErrMailContactInvalid)
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

// cleanContactProperties drops empty values and checks names and lengths. It returns a skip
// reason when the properties are unusable.
func cleanContactProperties(raw map[string]string) (map[string]string, string) {
	properties := make(map[string]string, len(raw))
	for name, value := range raw {
		if !validVariableName(name) || slices.Contains(reservedContactFields, name) {
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

// ImportContacts reads CSV text with a header row and creates or updates contacts by address.
// Unusable rows are skipped and reported; the rest are stored in one transaction.
func (s *service) ImportContacts(ctx context.Context, organizationID uint, text string, listID uint) (*ContactImportResult, error) {
	if len(text) > MaxContactImportBytes || !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: the file must be UTF-8 text of at most 5 MiB", ErrMailContactImportInvalid)
	}
	text = strings.TrimPrefix(text, "\uFEFF")
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: a header row is required", ErrMailContactImportInvalid)
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
				return nil, fmt.Errorf("%w: line %d is not valid CSV", ErrMailContactImportInvalid, parseErr.Line)
			}
			return nil, fmt.Errorf("%w: the file is not valid CSV", ErrMailContactImportInvalid)
		}
		if blankRecord(record) {
			continue
		}
		line, _ := reader.FieldPos(0)
		result.Rows++
		if result.Rows > MaxContactImportRows {
			return nil, fmt.Errorf("%w: at most %d rows", ErrMailContactImportInvalid, MaxContactImportRows)
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

// importColumn is what one CSV column holds.
type importColumn struct {
	field    string // email, first_name, last_name, or a property name; empty when ignored
	property bool
}

// importColumns maps header cells to fields. Names are compared case-insensitively, and spaces
// and hyphens count as underscores, so "First Name" is first_name. A column whose name cannot be
// a property is ignored and reported.
func importColumns(header []string) ([]importColumn, []string, error) {
	if len(header) > maxContactImportFields {
		return nil, nil, fmt.Errorf("%w: at most %d columns", ErrMailContactImportInvalid, maxContactImportFields)
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
			return nil, nil, fmt.Errorf("%w: column %q appears twice", ErrMailContactImportInvalid, raw)
		}
		seen[name] = struct{}{}
		switch {
		case name == "email":
			hasEmail = true
			columns[index] = importColumn{field: name}
		case name == "first_name" || name == "last_name":
			columns[index] = importColumn{field: name}
		case validVariableName(name):
			columns[index] = importColumn{field: name, property: true}
		default:
			if trimmed := strings.TrimSpace(raw); trimmed != "" {
				ignored = append(ignored, truncateText(cleanText(trimmed), 100))
			}
		}
	}
	if !hasEmail {
		return nil, nil, fmt.Errorf("%w: an email column is required", ErrMailContactImportInvalid)
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

func (s *service) ListContactLists(ctx context.Context, organizationID uint) ([]*domain.MailContactList, error) {
	return s.contacts.ListContactLists(ctx, organizationID)
}

func (s *service) CreateContactList(ctx context.Context, organizationID uint, name string) (*domain.MailContactList, error) {
	cleaned, ok := cleanListName(name)
	if !ok {
		return nil, fmt.Errorf("%w: name", ErrMailContactInvalid)
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
		return nil, fmt.Errorf("%w: name", ErrMailContactInvalid)
	}
	return s.contacts.RenameContactList(ctx, organizationID, id, cleaned)
}

func (s *service) DeleteContactList(ctx context.Context, organizationID, id uint) error {
	return s.contacts.DeleteContactList(ctx, organizationID, id)
}

func cleanListName(raw string) (string, bool) {
	name := strings.Join(strings.Fields(cleanText(raw)), " ")
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", false
	}
	return name, true
}
