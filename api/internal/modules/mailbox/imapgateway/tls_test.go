package imapgateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePair(t *testing.T, dir, name string, modTime time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{certFile, keyFile} {
		if err := os.Chtimes(file, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	return certFile, keyFile
}

func servedName(t *testing.T, r *CertificateReloader) string {
	t.Helper()
	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Subject.CommonName
}

func TestCertificateReloaderPicksUpARenewal(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Hour)
	certFile, keyFile := writePair(t, dir, "first", start)
	reloader, err := NewCertificateReloader(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	reloader.now = func() time.Time { return now }
	if name := servedName(t, reloader); name != "first" {
		t.Fatalf("served %q", name)
	}

	writePair(t, dir, "renewed", start.Add(time.Minute))
	if name := servedName(t, reloader); name != "first" {
		t.Fatalf("checked the files again within %s: served %q", certificateCheckInterval, name)
	}
	now = now.Add(certificateCheckInterval)
	if name := servedName(t, reloader); name != "renewed" {
		t.Fatalf("after the renewal served %q", name)
	}

	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(certificateCheckInterval)
	if name := servedName(t, reloader); name != "renewed" {
		t.Fatalf("a broken renewal replaced the certificate: served %q", name)
	}
	if _, err := NewCertificateReloader(filepath.Join(dir, "missing.pem"), keyFile); err == nil {
		t.Fatal("a missing certificate loaded")
	}
}
