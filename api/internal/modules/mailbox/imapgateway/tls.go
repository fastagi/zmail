package imapgateway

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"
)

// certificateCheckInterval is how often a handshake may look for a renewed certificate.
const certificateCheckInterval = 30 * time.Second

// CertificateReloader serves a PEM certificate and key from disk and picks up a renewed pair on
// the next handshake after the files change, without a restart.
type CertificateReloader struct {
	certFile, keyFile string
	now               func() time.Time

	mu        sync.Mutex
	current   *tls.Certificate
	certMod   time.Time
	keyMod    time.Time
	checkedAt time.Time
}

// NewCertificateReloader loads the pair once and fails when it cannot.
func NewCertificateReloader(certFile, keyFile string) (*CertificateReloader, error) {
	r := &CertificateReloader{certFile: certFile, keyFile: keyFile, now: time.Now}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CertificateReloader) reload() error {
	certInfo, err := os.Stat(r.certFile)
	if err != nil {
		return fmt.Errorf("mail gateway certificate: %w", err)
	}
	keyInfo, err := os.Stat(r.keyFile)
	if err != nil {
		return fmt.Errorf("mail gateway key: %w", err)
	}
	pair, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("mail gateway certificate: %w", err)
	}
	r.current, r.certMod, r.keyMod = &pair, certInfo.ModTime(), keyInfo.ModTime()
	return nil
}

// GetCertificate is tls.Config.GetCertificate. A renewed pair that does not load keeps the
// previous one in service.
func (r *CertificateReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if now.Sub(r.checkedAt) >= certificateCheckInterval {
		r.checkedAt = now
		certInfo, certErr := os.Stat(r.certFile)
		keyInfo, keyErr := os.Stat(r.keyFile)
		if certErr == nil && keyErr == nil && (!certInfo.ModTime().Equal(r.certMod) || !keyInfo.ModTime().Equal(r.keyMod)) {
			previous := r.current
			if err := r.reload(); err != nil {
				r.current = previous
			}
		}
	}
	return r.current, nil
}

// TLSConfig is the gateway's server TLS configuration: TLS 1.2 or later, certificate from r.
func (r *CertificateReloader) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: r.GetCertificate}
}
