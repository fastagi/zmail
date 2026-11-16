package mailbox

import (
	"net/mail"
	"regexp"
	"strings"
)

// localPartPattern allows lowercase letters, digits, and inner dots, underscores, and hyphens.
var localPartPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

// reservedLocalParts are role addresses that RFC 2142 and common practice give to the domain
// operator. Letting a user claim one would let them receive mail meant for the operator.
var reservedLocalParts = map[string]struct{}{
	"abuse": {}, "admin": {}, "administrator": {}, "hostmaster": {}, "mailer-daemon": {},
	"no-reply": {}, "noreply": {}, "postmaster": {}, "root": {}, "security": {},
	"support": {}, "webmaster": {},
}

// normalizeLocalPart validates the requested local part and returns its canonical form.
func normalizeLocalPart(raw string) (string, error) {
	localPart := strings.ToLower(strings.TrimSpace(raw))
	if !localPartPattern.MatchString(localPart) || strings.Contains(localPart, "..") {
		return "", ErrMailboxAddressInvalid
	}
	if _, reserved := reservedLocalParts[localPart]; reserved {
		return "", ErrMailboxAddressReserved
	}
	return localPart, nil
}

// normalizeRecipient accepts a bare address and returns it lowercased. Display names are
// rejected so the stored value is always exactly the delivery address.
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
	return strings.ToLower(parsed.Address), true
}

// normalizeRecipients validates, lowercases, and de-duplicates addresses, keeping order.
func normalizeRecipients(raw []string, seen map[string]struct{}) ([]string, bool) {
	recipients := make([]string, 0, len(raw))
	for _, candidate := range raw {
		address, ok := normalizeRecipient(candidate)
		if !ok {
			return nil, false
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		recipients = append(recipients, address)
	}
	return recipients, true
}

// normalizeMessageID strips angle brackets from an RFC 5322 message identifier.
func normalizeMessageID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	if id == "" {
		return "", true
	}
	if len(id) > 900 || strings.ContainsAny(id, " \t\r\n<>") || !strings.Contains(id, "@") {
		return "", false
	}
	return id, true
}
