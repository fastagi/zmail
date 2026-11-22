package mailbox

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// fakeSMTPServer is an implicit-TLS SMTP server that accepts one login and records each message.
type fakeSMTPServer struct {
	t          *testing.T
	listener   net.Listener
	roots      *x509.CertPool
	cert       *x509.Certificate
	password   string
	port       int
	mu         sync.Mutex
	from       string
	rcpts      []string
	data       string
	authPlain  bool
	rejectRcpt bool
}

const fakeSMTPPassword = "smtp-app-password"

func newFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "smtp.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"smtp.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSMTPServer{t: t, listener: listener, roots: roots, cert: certificate, port: listener.Addr().(*net.TCPAddr).Port, authPlain: true, password: fakeSMTPPassword}
	t.Cleanup(func() { _ = listener.Close() })
	go server.serve()
	return server
}

func (f *fakeSMTPServer) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.session(conn)
	}
}

func (f *fakeSMTPServer) session(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	read := func() string {
		line, _ := reader.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	write("220 smtp.test ready")
	for {
		line := read()
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		f.mu.Lock()
		plain, rejectRcpt := f.authPlain, f.rejectRcpt
		f.mu.Unlock()
		switch {
		case verb == "EHLO":
			write("250-smtp.test")
			if plain {
				write("250 AUTH PLAIN LOGIN")
			} else {
				write("250 AUTH LOGIN")
			}
		case verb == "AUTH" && strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[2] == f.password {
				write("235 ok")
			} else {
				write("535 bad credentials")
			}
		case verb == "AUTH":
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			_ = read()
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			password, _ := base64.StdEncoding.DecodeString(read())
			if string(password) == f.password {
				write("235 ok")
			} else {
				write("535 bad credentials")
			}
		case verb == "MAIL":
			f.mu.Lock()
			f.from, f.rcpts = line, nil
			f.mu.Unlock()
			write("250 ok")
		case verb == "RCPT":
			if rejectRcpt {
				write("550 no such user")
				continue
			}
			f.mu.Lock()
			f.rcpts = append(f.rcpts, line)
			f.mu.Unlock()
			write("250 ok")
		case verb == "DATA":
			write("354 go ahead")
			var body strings.Builder
			for {
				l := read()
				if l == "." {
					break
				}
				body.WriteString(l + "\n")
			}
			f.mu.Lock()
			f.data = body.String()
			f.mu.Unlock()
			write("250 queued")
		case verb == "QUIT":
			write("221 bye")
			return
		case verb == "":
			return
		default:
			write("250 ok")
		}
	}
}

func (f *fakeSMTPServer) source() *imapSource {
	source := newIMAPSource()
	source.policy = imapNetworkPolicy{AllowPrivate: true, AllowAnyPort: true, RootCAs: f.roots}
	source.dialTimeout, source.overallTimeout = 5*time.Second, 10*time.Second
	source.resolver = fixedResolver{"smtp.test": []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	return source
}

func (f *fakeSMTPServer) grant(password string) ExternalMailToken {
	raw, _ := json.Marshal(imapCredentials{
		Host: "imap.test", Port: imapPort, Username: "member@example.com", Password: password,
		SMTPHost: "smtp.test", SMTPPort: f.port,
	})
	return ExternalMailToken{RefreshToken: string(raw)}
}

func TestIMAPSourceSendMailDeliversThroughTheAccountServer(t *testing.T) {
	server := newFakeSMTPServer(t)
	for _, plain := range []bool{true, false} {
		server.mu.Lock()
		server.authPlain = plain
		server.mu.Unlock()
		result, err := server.source().SendMail(context.Background(), server.grant(fakeSMTPPassword), mailtransport.Message{
			FromAddress: "member@example.com", FromName: "Member", To: []string{"a@example.org"}, Cc: []string{"b@example.org"},
			Subject: "Hello 你好", TextBody: "plain body", HTMLBody: "<p>html body</p>", MessageID: "id-1@example.com",
			InReplyTo: "parent@example.org", References: []string{"root@example.org", "parent@example.org"},
			Attachments: []mailtransport.Attachment{{Filename: "note.txt", ContentType: "text/plain", Content: []byte("attached")}},
		})
		if err != nil {
			t.Fatalf("plain=%v: send: %v", plain, err)
		}
		if result.RFCMessageID != "id-1@example.com" {
			t.Fatalf("message id = %q", result.RFCMessageID)
		}
		server.mu.Lock()
		data, rcpts, from := server.data, server.rcpts, server.from
		server.mu.Unlock()
		if !strings.Contains(from, "<member@example.com>") || len(rcpts) != 2 {
			t.Fatalf("envelope from=%q rcpts=%v", from, rcpts)
		}
		for _, want := range []string{
			"Message-ID: <id-1@example.com>", "In-Reply-To: <parent@example.org>",
			"References: <root@example.org> <parent@example.org>", "Cc: <b@example.org>",
			"multipart/mixed", "multipart/alternative", "filename=\"note.txt\"", "Subject: =?utf-8?q?Hello_",
		} {
			if !strings.Contains(data, want) {
				t.Fatalf("plain=%v: message lacks %q:\n%s", plain, want, data)
			}
		}
	}
}

func TestIMAPSourceSendMailClassifiesRefusals(t *testing.T) {
	server := newFakeSMTPServer(t)
	_, err := server.source().SendMail(context.Background(), server.grant("wrong"), mailtransport.Message{
		FromAddress: "member@example.com", To: []string{"a@example.org"}, Subject: "s", TextBody: "t",
	})
	var transportErr *mailtransport.Error
	if !errors.As(err, &transportErr) || transportErr.Code != failureSMTPAuth || !transportErr.Permanent {
		t.Fatalf("wrong password: %v", err)
	}
	server.mu.Lock()
	server.rejectRcpt = true
	server.mu.Unlock()
	_, err = server.source().SendMail(context.Background(), server.grant(fakeSMTPPassword), mailtransport.Message{
		FromAddress: "member@example.com", To: []string{"a@example.org"}, Subject: "s", TextBody: "t",
	})
	if !errors.As(err, &transportErr) || transportErr.Code != failureSMTPRejected || !transportErr.Permanent {
		t.Fatalf("rejected recipient: %v", err)
	}
	_, err = server.source().SendMail(context.Background(), ExternalMailToken{RefreshToken: `{"host":"imap.test"}`}, mailtransport.Message{})
	if !errors.As(err, &transportErr) || transportErr.Code != failureSMTPNotBound {
		t.Fatalf("grant without SMTP: %v", err)
	}
}

func TestIMAPSourceVerifySMTP(t *testing.T) {
	server := newFakeSMTPServer(t)
	credentials := imapCredentials{Username: "member@example.com", Password: fakeSMTPPassword, SMTPHost: "smtp.test", SMTPPort: server.port}
	if err := server.source().verifySMTP(context.Background(), credentials); err != nil {
		t.Fatalf("verify: %v", err)
	}
	credentials.Password = "wrong"
	if err := server.source().verifySMTP(context.Background(), credentials); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("wrong password: %v", err)
	}
	credentials.SMTPHost = "10.0.0.1"
	source := server.source()
	source.policy.AllowPrivate = false
	if err := source.verifySMTP(context.Background(), credentials); !errors.Is(err, ErrExternalMailServerUnreachable) {
		t.Fatalf("private host: %v", err)
	}
}

func TestIMAPSourceRefusesOtherSMTPPorts(t *testing.T) {
	server := newFakeSMTPServer(t)
	source := server.source()
	source.policy.AllowAnyPort = false
	credentials := imapCredentials{Username: "member@example.com", Password: fakeSMTPPassword, SMTPHost: "smtp.test", SMTPPort: 25}
	if err := source.verifySMTP(context.Background(), credentials); !errors.Is(err, ErrExternalMailServerUnreachable) {
		t.Fatalf("port 25: %v", err)
	}
}

func TestBuildMIMEMessageRefusesHeaderInjection(t *testing.T) {
	if _, err := buildMIMEMessage(mailtransport.Message{FromAddress: "a@b.c", Subject: "x\r\nBcc: e@f.g", TextBody: "t"}, "id@b.c", time.Now()); err == nil {
		t.Fatal("a subject with a line break must be refused")
	}
	raw, err := buildMIMEMessage(mailtransport.Message{FromAddress: "a@b.c", To: []string{"d@e.f"}, Subject: "s", TextBody: "line1\nline2"}, "id@b.c", time.Now())
	if err != nil || !strings.Contains(string(raw), "Content-Type: text/plain; charset=utf-8") || !strings.Contains(string(raw), "line1\r\nline2") {
		t.Fatalf("plain message: %v\n%s", err, raw)
	}
}
