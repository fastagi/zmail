package mailbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Inbound delivery headers set by the mail edge.
const (
	InboundSignatureHeader = "X-Mailbox-Signature"
	InboundRecipientHeader = "X-Mailbox-Recipient"
)

// signInbound computes the delivery signature: HMAC-SHA256 over
// "<unix seconds>.<lowercase recipient>.<raw message>".
func signInbound(secret string, timestamp int64, recipient string, raw []byte) string {
	return hex.EncodeToString(inboundMAC(secret, timestamp, recipient, raw))
}

func inboundMAC(secret string, timestamp int64, recipient string, raw []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte{'.'})
	mac.Write([]byte(strings.ToLower(recipient)))
	mac.Write([]byte{'.'})
	mac.Write(raw)
	return mac.Sum(nil)
}

// verifyInbound checks a "t=<unix seconds>,v1=<hex>" header in constant time and rejects
// deliveries whose timestamp is outside the tolerance, which bounds replay of a captured request.
func verifyInbound(secret, header, recipient string, raw []byte, now time.Time, tolerance time.Duration) bool {
	if secret == "" {
		return false
	}
	var (
		timestamp int64
		signature string
		hasTime   bool
	)
	for _, field := range strings.Split(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(field), "=")
		if !found {
			return false
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return false
			}
			timestamp, hasTime = parsed, true
		case "v1":
			signature = value
		}
	}
	if !hasTime || signature == "" {
		return false
	}
	skew := now.Sub(time.Unix(timestamp, 0))
	if skew < -tolerance || skew > tolerance {
		return false
	}
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(provided, inboundMAC(secret, timestamp, recipient, raw))
}
