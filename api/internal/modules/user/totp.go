package user

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 authenticator apps use HMAC-SHA1.
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

const (
	totpPeriod      = 30
	totpDigits      = 6
	totpSecretBytes = 20
	// totpSkew accepts the previous and next step for clock drift.
	totpSkew = 1
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return totpEncoding.EncodeToString(raw), nil
}

func totpStep(at time.Time) int64 {
	return at.Unix() / totpPeriod
}

func totpCode(secret string, step int64) (string, error) {
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("decode totp secret: %w", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step)) //nolint:gosec // steps are positive Unix times.
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000), nil
}

// matchTOTP returns the step a code matches within the skew window and after lastUsed; ok is
// false for a wrong, malformed, or replayed code.
func matchTOTP(secret, code string, at time.Time, lastUsed int64) (step int64, ok bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	now := totpStep(at)
	for candidate := now - totpSkew; candidate <= now+totpSkew; candidate++ {
		if candidate <= lastUsed {
			continue
		}
		expected, err := totpCode(secret, candidate)
		if err == nil && hmac.Equal([]byte(expected), []byte(code)) {
			return candidate, true
		}
	}
	return 0, false
}

func totpURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"},
		"digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(totpPeriod)}}
	return "otpauth://totp/" + label + "?" + query.Encode()
}
