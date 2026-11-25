package transactional

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"
)

const (
	// MaskedSecret replaces a secret value that is not a web URL.
	MaskedSecret = "••••••"
	// MaskedURLPath replaces everything of a secret URL after its host.
	MaskedURLPath = "/••••"
	// MaskedLocalPart follows the kept start of a masked recipient's local part.
	MaskedLocalPart = "••"
	// fingerprintScope separates the fingerprint key from any other use of the encryption key.
	fingerprintScope = "transactional-idempotency"
)

var simulatorOutcomes = map[string]struct{}{
	"delivered": {}, "bounced": {}, "soft-bounced": {}, "complained": {}, "suppressed": {},
}

// MaskSecret masks a secret variable: an http/https URL shows only host + /••••, everything else shows six dots.
func MaskSecret(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return MaskedSecret
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" {
		return MaskedSecret
	}
	return host + MaskedURLPath
}

// MaskRecipient keeps the domain and the first one or two characters of the local part.
func MaskRecipient(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return MaskedLocalPart
	}
	local := []rune(address[:at])
	keep := 2
	if len(local) <= 2 {
		keep = 1
	}
	if keep > len(local) {
		keep = len(local)
	}
	return string(local[:keep]) + MaskedLocalPart + address[at:]
}

// FingerprintKey derives the idempotency fingerprint key from the encryption key.
func FingerprintKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	mac := hmac.New(sha256.New, sum[:])
	mac.Write([]byte(fingerprintScope))
	return mac.Sum(nil)
}

// Fingerprint identifies the content of a transactional send.
func Fingerprint(key []byte, templateKey, to string, variables map[string]string) []byte {
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

// DirectFingerprint identifies the content of a direct (template-free) transactional send.
func DirectFingerprint(key []byte, fromAddress, fromName, to, subject, html, text string) []byte {
	encoded, _ := json.Marshal(struct { //nolint:errcheck // strings always encode
		Kind        string `json:"kind"`
		FromAddress string `json:"from_address"`
		FromName    string `json:"from_name"`
		To          string `json:"to"`
		Subject     string `json:"subject"`
		HTML        string `json:"html"`
		Text        string `json:"text"`
	}{"direct", fromAddress, fromName, to, subject, html, text})
	mac := hmac.New(sha256.New, key)
	mac.Write(encoded)
	return mac.Sum(nil)
}

// SimulatorOutcome parses a recipient on the simulator domain.
func SimulatorOutcome(to, simulatorDomain string) (outcome string, simulated bool, ok bool) {
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
