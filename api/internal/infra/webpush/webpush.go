// Package webpush sends Web Push messages: RFC 8291 payload encryption (aes128gcm) with
// RFC 8292 VAPID authentication. It uses only the standard library and x/crypto.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

const (
	recordSize = 4096
	// MaxPayloadBytes keeps every message in one record with room for the header and padding
	// delimiter, and under the 4 KiB limit push services enforce.
	MaxPayloadBytes = 3000
	tokenLifetime   = 12 * time.Hour
)

var (
	// ErrInvalidSubscription marks subscription keys or endpoints that can never work.
	ErrInvalidSubscription = errors.New("webpush: invalid subscription")
	// ErrPayloadTooLarge marks payloads above MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("webpush: payload too large")
)

// Keys are the browser-provided subscription keys, base64url encoded.
type Keys struct {
	P256dh string
	Auth   string
}

// Encrypt encrypts a payload for one subscription with a fresh sender key and salt.
func Encrypt(payload []byte, keys Keys) ([]byte, error) {
	sender, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encryptWith(payload, keys, sender, salt, recordSize)
}

func encryptWith(payload []byte, keys Keys, sender *ecdh.PrivateKey, salt []byte, rs uint32) ([]byte, error) {
	if len(payload) > MaxPayloadBytes {
		return nil, ErrPayloadTooLarge
	}
	uaPublicBytes, err := decodeKey(keys.P256dh)
	if err != nil {
		return nil, ErrInvalidSubscription
	}
	uaPublic, err := ecdh.P256().NewPublicKey(uaPublicBytes)
	if err != nil {
		return nil, ErrInvalidSubscription
	}
	auth, err := decodeKey(keys.Auth)
	if err != nil || len(auth) != 16 {
		return nil, ErrInvalidSubscription
	}
	shared, err := sender.ECDH(uaPublic)
	if err != nil {
		return nil, ErrInvalidSubscription
	}
	asPublic := sender.PublicKey().Bytes()

	info := make([]byte, 0, 14+65+65)
	info = append(info, "WebPush: info\x00"...)
	info = append(info, uaPublicBytes...)
	info = append(info, asPublic...)
	ikm, err := expand(shared, auth, info, 32)
	if err != nil {
		return nil, err
	}
	cek, err := expand(ikm, salt, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, err
	}
	nonce, err := expand(ikm, salt, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// A single, final record: the payload followed by the 0x02 delimiter.
	plain := append(append([]byte{}, payload...), 0x02)

	header := make([]byte, 0, 21+len(asPublic))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, rs)
	header = append(header, byte(len(asPublic)))
	header = append(header, asPublic...)
	return gcm.Seal(header, nonce, plain, nil), nil
}

func expand(secret, salt, info []byte, size int) ([]byte, error) {
	out := make([]byte, size)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, info), out); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeKey(value string) ([]byte, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "=")
	if value == "" {
		return nil, ErrInvalidSubscription
	}
	if out, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return out, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

// VAPIDKeys is a base64url-encoded application server key pair.
type VAPIDKeys struct {
	PublicKey  string
	PrivateKey string
}

// GenerateVAPIDKeys creates a new P-256 application server key pair.
func GenerateVAPIDKeys() (VAPIDKeys, error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return VAPIDKeys{}, err
	}
	return VAPIDKeys{
		PublicKey:  base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		PrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes()),
	}, nil
}

// VAPID signs RFC 8292 authorization headers for one application server key.
type VAPID struct {
	publicKey string
	subject   string
	key       *ecdsa.PrivateKey
}

// NewVAPID validates a key pair and contact subject (mailto: or https:).
func NewVAPID(publicKey, privateKey, subject string) (*VAPID, error) {
	if !strings.HasPrefix(subject, "mailto:") && !strings.HasPrefix(subject, "https://") {
		return nil, errors.New("webpush: VAPID subject must start with mailto: or https://")
	}
	priv, err := decodeKey(privateKey)
	if err != nil {
		return nil, errors.New("webpush: invalid VAPID private key")
	}
	ecdhKey, err := ecdh.P256().NewPrivateKey(priv)
	if err != nil {
		return nil, errors.New("webpush: invalid VAPID private key")
	}
	pub, err := decodeKey(publicKey)
	if err != nil || !bytes.Equal(pub, ecdhKey.PublicKey().Bytes()) {
		return nil, errors.New("webpush: VAPID public key does not match the private key")
	}
	key := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(pub[1:33]), Y: new(big.Int).SetBytes(pub[33:])},
		D:         new(big.Int).SetBytes(priv),
	}
	return &VAPID{publicKey: base64.RawURLEncoding.EncodeToString(pub), subject: subject, key: key}, nil
}

// PublicKey returns the base64url application server key browsers subscribe with.
func (v *VAPID) PublicKey() string { return v.publicKey }

// Authorization returns the `vapid t=…, k=…` header value for a push endpoint.
func (v *VAPID) Authorization(endpoint string, now time.Time) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", ErrInvalidSubscription
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": parsed.Scheme + "://" + parsed.Host,
		"exp": now.Add(tokenLifetime).Unix(),
		"sub": v.subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signingInput + "." + base64.RawURLEncoding.EncodeToString(sig) + ", k=" + v.publicKey, nil
}

// Result classifies one push attempt.
type Result int

const (
	// Delivered means the push service accepted the message.
	Delivered Result = iota
	// Gone means the subscription expired or was revoked and must be removed.
	Gone
	// Retry means a transient failure worth retrying later.
	Retry
	// Rejected means the push service refused the request permanently.
	Rejected
)

// Message is one push for one subscription.
type Message struct {
	Endpoint string
	Keys     Keys
	Payload  []byte
	TTL      time.Duration
	// Topic lets the push service replace an undelivered message with the same topic.
	Topic   string
	Urgency string
}

// Client sends encrypted pushes.
type Client struct {
	vapid *VAPID
	http  *http.Client
	now   func() time.Time
}

// NewClient builds a sender. httpClient may be nil.
func NewClient(vapid *VAPID, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{vapid: vapid, http: httpClient, now: time.Now}
}

// PublicKey returns the application server key.
func (c *Client) PublicKey() string { return c.vapid.PublicKey() }

// Send encrypts and posts one message. Errors are returned only together with Retry or Rejected.
func (c *Client) Send(ctx context.Context, msg Message) (Result, error) {
	body, err := Encrypt(msg.Payload, msg.Keys)
	if err != nil {
		return Rejected, err
	}
	auth, err := c.vapid.Authorization(msg.Endpoint, c.now())
	if err != nil {
		return Rejected, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, msg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Rejected, ErrInvalidSubscription
	}
	ttl := msg.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(ttl/time.Second)))
	if msg.Topic != "" {
		req.Header.Set("Topic", msg.Topic)
	}
	if msg.Urgency != "" {
		req.Header.Set("Urgency", msg.Urgency)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Retry, fmt.Errorf("webpush: push service unreachable: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return Delivered, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Gone, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return Retry, fmt.Errorf("webpush: push service status %d", resp.StatusCode)
	default:
		return Rejected, fmt.Errorf("webpush: push service status %d", resp.StatusCode)
	}
}

// pushServiceSuffixes lists the browser push services. Subscriptions pointing anywhere else are
// refused so a stored endpoint can never make the server call an arbitrary host.
var pushServiceSuffixes = []string{
	"fcm.googleapis.com",
	"push.services.mozilla.com",
	"push.apple.com",
	"notify.windows.com",
}

// AllowedEndpoint reports whether an endpoint is an https URL on a known browser push service.
func AllowedEndpoint(endpoint string) bool {
	if len(endpoint) > 2048 {
		return false
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range pushServiceSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
