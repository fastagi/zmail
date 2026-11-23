package operatorcommands

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/console"
	infracommands "github.com/fastagi/zmail/api/internal/infra/console/commands"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

func TestMailGatewayCommandRegistered(t *testing.T) {
	application := console.New("fastbuild", "test")
	infracommands.RegisterManifest(application, Manifest())
	assert.True(t, application.HasCommand("mail:gateway"))
}

// TestMailGatewayDisabledExitsCleanly: with MAIL_GATEWAY_IMAP_ADDR empty the command exits before
// it initializes the application or opens the database.
func TestMailGatewayDisabledExitsCleanly(t *testing.T) {
	command := NewMailGatewayCommand()
	command.loadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	require.NoError(t, command.Run(nil))
	require.Error(t, command.Run([]string{"--port=993"}))
}

// refusingClientStore refuses every sign-in; the run test never gets further.
type refusingClientStore struct{ domain.MailClientStore }

func (refusingClientStore) AuthenticateClient(context.Context, string, string, domain.MailClientProtocol, string) (*domain.MailClientPrincipal, error) {
	return nil, domain.ErrMailClientAuthFailed
}

func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "imap.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}

func TestRunMailGatewayServesAndStops(t *testing.T) {
	certFile, keyFile := writeTestCertificate(t)
	cfg := config.MailGatewayConfig{IMAPAddr: freeAddr(t), HealthAddr: freeAddr(t), TLSCertFile: certFile, TLSKeyFile: keyFile}.Resolved()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runMailGateway(ctx, cfg, refusingClientStore{}, ratelimit.NewMemoryStores(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	var conn *tls.Conn
	require.Eventually(t, func() bool {
		var err error
		conn, err = tls.Dial("tcp", cfg.IMAPAddr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the test's own certificate
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	greeting := make([]byte, 64)
	n, err := conn.Read(greeting)
	require.NoError(t, err)
	assert.Contains(t, string(greeting[:n]), "* OK")
	assert.GreaterOrEqual(t, conn.ConnectionState().Version, uint16(tls.VersionTLS12))

	response, err := http.Get("http://" + cfg.HealthAddr + "/health") //nolint:noctx // a local test request
	require.NoError(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("the gateway did not stop")
	}
	rest, _ := io.ReadAll(conn)
	assert.Contains(t, string(rest), "* BYE")
}
