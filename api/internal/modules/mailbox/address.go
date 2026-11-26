package mailbox

import (
	"net/mail"
	"regexp"
	"slices"
	"strings"
)

// localPartPattern allows lowercase letters, digits, and inner dots, underscores, and hyphens.
var localPartPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

// reservedLocalParts are role addresses that RFC 2142 and common practice give to the domain
// operator. On the shared domain the operator is Zmail, so letting a user claim one would let them
// receive mail meant for the operator. An organization's own domain is operated by the
// organization, which may claim them.
var reservedLocalParts = map[string]struct{}{
	"abuse": {}, "admin": {}, "administrator": {}, "hostmaster": {}, "mailer-daemon": {},
	"no-reply": {}, "noreply": {}, "postmaster": {}, "root": {}, "security": {},
	"support": {}, "webmaster": {},
}

// publicReservedLocalParts are additional brand, official, system, and high-risk handles blocked
// for self-serve public mailbox claims (unioned with reservedLocalParts).
var publicReservedLocalParts = map[string]struct{}{
	"zmail": {}, "official": {}, "staff": {}, "team": {}, "system": {}, "billing": {},
	"finance": {}, "help": {}, "info": {}, "contact": {}, "ceo": {}, "cto": {},
	"founder": {}, "owner": {}, "ops": {}, "devops": {}, "mail": {}, "smtp": {},
	"imap": {}, "pop": {}, "api": {}, "auth": {}, "login": {}, "register": {},
	"verify": {}, "service": {}, "notice": {}, "notify": {}, "alert": {}, "hr": {},
	"legal": {}, "privacy": {}, "terms": {}, "compliance": {}, "dmarc": {}, "spf": {},
	"dkim": {}, "feedback": {}, "sales": {}, "press": {}, "hello": {}, "hi": {},
	"me": {}, "ai": {}, "app": {}, "dev": {}, "test": {}, "demo": {},
	"www": {}, "ftp": {}, "ms": {}, "z": {}, "status": {}, "cloud": {},
	"docs": {}, "blog": {}, "news": {}, "vip": {}, "pro": {}, "enterprise": {},
}

// isPublicReservedLocalPart reports whether localPart is reserved for operator or brand protection
// on self-serve public mailbox claims.
func isPublicReservedLocalPart(localPart string) bool {
	if _, reserved := reservedLocalParts[localPart]; reserved {
		return true
	}
	_, reserved := publicReservedLocalParts[localPart]
	return reserved
}

// normalizeLocalPart validates the requested local part and returns its canonical form. Whether a
// role address is free depends on the domain; see checkReservedLocalPart.
func normalizeLocalPart(raw string) (string, error) {
	localPart := strings.ToLower(strings.TrimSpace(raw))
	if !localPartPattern.MatchString(localPart) || strings.Contains(localPart, "..") {
		return "", ErrMailboxAddressInvalid
	}
	return localPart, nil
}

// checkReservedLocalPart answers ErrMailboxAddressReserved for a role address on any shared
// domain. Role addresses on an organization's own domain are allowed.
func checkReservedLocalPart(localPart, addressDomain string, sharedDomains ...string) error {
	if !slices.Contains(sharedDomains, addressDomain) {
		return nil
	}
	if _, reserved := reservedLocalParts[localPart]; reserved {
		return ErrMailboxAddressReserved
	}
	return nil
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
	if !withinSMTPLimits(parsed.Address, at) {
		return "", false
	}
	return strings.ToLower(parsed.Address), true
}

// RFC 5321 section 4.5.3.1 limits.
const (
	maxAddressLength   = 254
	maxLocalPartLength = 64
	maxDomainLabel     = 63
)

func withinSMTPLimits(address string, at int) bool {
	if len(address) > maxAddressLength || at > maxLocalPartLength {
		return false
	}
	for _, label := range strings.Split(address[at+1:], ".") {
		if label == "" || len(label) > maxDomainLabel {
			return false
		}
	}
	return true
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
