package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"
)

func b64(t *testing.T, value string) []byte {
	t.Helper()
	out, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode %q: %v", value, err)
	}
	return out
}

// RFC 8291 Appendix A.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcBody      = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func TestEncryptMatchesRFC8291Example(t *testing.T) {
	sender, err := ecdh.P256().NewPrivateKey(b64(t, rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encryptWith(
		[]byte(rfcPlaintext),
		Keys{P256dh: rfcUAPublic, Auth: rfcAuth},
		sender,
		b64(t, rfcSalt),
		4096,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := b64(t, rfcBody); !bytes.Equal(got, want) {
		t.Fatalf("body mismatch\n got %s\nwant %s", base64.RawURLEncoding.EncodeToString(got), rfcBody)
	}
}

// decrypt is the user agent's side of RFC 8291, used to prove a random-key encryption round-trips.
func decrypt(t *testing.T, body []byte, uaPrivate *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt := body[:16]
	idlen := int(body[20])
	asPublicBytes := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]
	asPublic, err := ecdh.P256().NewPublicKey(asPublicBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		t.Fatal(err)
	}
	info := append([]byte("WebPush: info\x00"), uaPrivate.PublicKey().Bytes()...)
	info = append(info, asPublicBytes...)
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, auth, info), ikm); err != nil {
		t.Fatal(err)
	}
	cek := make([]byte, 16)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), cek); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(plain[:len(plain)-1], "\x00")
}

func TestEncryptRoundTripsWithAFreshSenderKey(t *testing.T) {
	ua, err := ecdh.P256().NewPrivateKey(b64(t, rfcUAPrivate))
	if err != nil {
		t.Fatal(err)
	}
	body, err := Encrypt([]byte(`{"title":"hi"}`), Keys{P256dh: rfcUAPublic, Auth: rfcAuth})
	if err != nil {
		t.Fatal(err)
	}
	if got := decrypt(t, body, ua, b64(t, rfcAuth)); string(got) != `{"title":"hi"}` {
		t.Fatalf("round trip = %q", got)
	}
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Fatalf("record size = %d", rs)
	}
}

func TestEncryptRejectsMalformedSubscriptionKeys(t *testing.T) {
	cases := []Keys{
		{P256dh: "", Auth: rfcAuth},
		{P256dh: rfcUAPublic, Auth: ""},
		{P256dh: "not-a-key", Auth: rfcAuth},
		{P256dh: rfcUAPublic, Auth: "c2hvcnQ"},
	}
	for _, keys := range cases {
		if _, err := Encrypt([]byte("x"), keys); err == nil {
			t.Fatalf("expected error for %+v", keys)
		}
	}
}

func TestEncryptRejectsPayloadsThatDoNotFitOneRecord(t *testing.T) {
	if _, err := Encrypt(bytes.Repeat([]byte("a"), MaxPayloadBytes+1), Keys{P256dh: rfcUAPublic, Auth: rfcAuth}); err == nil {
		t.Fatal("expected oversized payload to fail")
	}
}

func TestVAPIDHeaderIsAVerifiableES256Token(t *testing.T) {
	keys, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewVAPID(keys.PublicKey, keys.PrivateKey, "mailto:ops@zmail.im")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	header, err := signer.Authorization("https://fcm.googleapis.com/fcm/send/abc", now)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "vapid t="
	if !strings.HasPrefix(header, prefix) {
		t.Fatalf("header = %q", header)
	}
	tokenAndKey := strings.SplitN(strings.TrimPrefix(header, prefix), ", k=", 2)
	if len(tokenAndKey) != 2 || tokenAndKey[1] != keys.PublicKey {
		t.Fatalf("header = %q", header)
	}
	parts := strings.Split(tokenAndKey[0], ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q", tokenAndKey[0])
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != "https://fcm.googleapis.com" || claims.Sub != "mailto:ops@zmail.im" || claims.Exp != now.Add(12*time.Hour).Unix() {
		t.Fatalf("claims = %+v", claims)
	}
	pub := b64(t, keys.PublicKey)
	verifyKey := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(pub[1:33]), Y: new(big.Int).SetBytes(pub[33:])}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig := b64(t, parts[2])
	if len(sig) != 64 || !ecdsa.Verify(verifyKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
}

func TestNewVAPIDRejectsMismatchedKeys(t *testing.T) {
	a, _ := GenerateVAPIDKeys()
	b, _ := GenerateVAPIDKeys()
	if _, err := NewVAPID(a.PublicKey, b.PrivateKey, "mailto:ops@zmail.im"); err == nil {
		t.Fatal("expected mismatched key pair to fail")
	}
	if _, err := NewVAPID(a.PublicKey, a.PrivateKey, "ops@zmail.im"); err == nil {
		t.Fatal("expected a subject without mailto: or https: to fail")
	}
}
