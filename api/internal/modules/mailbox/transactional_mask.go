package mailbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"
)

const (
	// maskedSecret replaces a secret value that is not a web URL; its length says nothing about
	// the value's.
	maskedSecret = "••••••"
	// maskedURLPath replaces everything of a secret URL after its host.
	maskedURLPath = "/••••"
	// maskedLocalPart follows the kept start of a masked recipient's local part.
	maskedLocalPart = "••"
	// transactionalFingerprintScope separates the fingerprint key from any other use of the
	// encryption key.
	transactionalFingerprintScope = "transactional-idempotency"
)

// simulatorOutcomes are the local parts of the simulator domain and the outcome each simulates.
var simulatorOutcomes = map[string]struct{}{
	"delivered": {}, "bounced": {}, "soft-bounced": {}, "complained": {}, "suppressed": {},
}

// maskSecret is the value of a secret variable as anyone but the dispatcher sees it: an http or
// https URL with a host shows only the host, without user information, port, path, or query;
// anything else shows six dots.
func maskSecret(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return maskedSecret
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" {
		return maskedSecret
	}
	return host + maskedURLPath
}

// maskRecipient keeps the domain and the first two characters of the local part (one when the
// local part has at most two), followed by two dots: lilei@example.com shows li••@example.com.
func maskRecipient(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return maskedLocalPart
	}
	local := []rune(address[:at])
	keep := 2
	if len(local) <= 2 {
		keep = 1
	}
	if keep > len(local) {
		keep = len(local)
	}
	return string(local[:keep]) + maskedLocalPart + address[at:]
}

// transactionalFingerprintKey derives the idempotency fingerprint key from the encryption key, so
// the fingerprint of a six-digit code cannot be reversed by trying every code without that key.
func transactionalFingerprintKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	mac := hmac.New(sha256.New, sum[:])
	mac.Write([]byte(transactionalFingerprintScope))
	return mac.Sum(nil)
}

// transactionalFingerprint identifies the content of a send: the template key, the normalized
// recipient, and every variable value, secrets included. JSON sorts map keys, so the variables'
// order never matters.
func transactionalFingerprint(key []byte, templateKey, to string, variables map[string]string) []byte {
	if variables == nil {
		variables = map[string]string{}
	}
	encoded, _ := json.Marshal(struct { //nolint:errcheck // strings and a string map always encode
		Template  string            `json:"template"`
		To        string            `json:"to"`
		Variables map[string]string `json:"variables"`
	}{templateKey, to, variables})
	mac := hmac.New(sha256.New, key)
	mac.Write(encoded)
	return mac.Sum(nil)
}

// simulatorOutcome reads a recipient on the simulator domain: simulated is true for any address
// there, ok is false when its local part (without a +tag) names no outcome. An address elsewhere
// is not simulated and always ok.
func simulatorOutcome(to, simulatorDomain string) (outcome string, simulated bool, ok bool) {
	to = strings.ToLower(strings.TrimSpace(to))
	at := strings.LastIndex(to, "@")
	if at < 0 || simulatorDomain == "" || to[at+1:] != strings.ToLower(simulatorDomain) {
		return "", false, true
	}
	local := to[:at]
	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	if _, known := simulatorOutcomes[local]; !known {
		return "", true, false
	}
	return local, true, true
}
