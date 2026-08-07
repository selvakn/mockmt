package mockmt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// generateSelfSignedCert creates an in-process self-signed certificate for
// 127.0.0.1 and returns a server tls.Config presenting it, a client
// tls.Config trusting it, and the parsed leaf certificate (for tests that
// need the raw certificate, e.g. to write it out as a CA file). Tests use
// this to exercise the production STARTTLS/TLS relay path (research R18)
// rather than a plaintext bypass.
func generateSelfSignedCert(t *testing.T) (serverConfig, clientConfig *tls.Config, leaf *x509.Certificate) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return &tls.Config{Certificates: []tls.Certificate{cert}}, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}, leaf
}

// fakeUpstreamOptions configures the behaviour of startFakeUpstream.
type fakeUpstreamOptions struct {
	// RejectRecipient, if set, causes RCPT TO for this exact address to be
	// refused with a permanent error. Other recipients are accepted.
	RejectRecipient string
	// HangOnData, if true, causes the fake server to read the message body
	// in full (the client sees the terminating dot accepted at the wire
	// level) but never send the final acknowledgement -- simulating the
	// indeterminate case where the message may have been delivered but the
	// sender never found out (research R5).
	HangOnData bool

	ctx context.Context
}

type fakeUpstreamBackend struct{ opts fakeUpstreamOptions }

func (b *fakeUpstreamBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &fakeUpstreamSession{opts: b.opts}, nil
}

type fakeUpstreamSession struct{ opts fakeUpstreamOptions }

func (s *fakeUpstreamSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *fakeUpstreamSession) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		return nil
	}), nil
}

func (s *fakeUpstreamSession) Mail(from string, opts *smtp.MailOptions) error { return nil }

func (s *fakeUpstreamSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	if s.opts.RejectRecipient != "" && to == s.opts.RejectRecipient {
		return &smtp.SMTPError{Code: 550, Message: "no such recipient"}
	}
	return nil
}

func (s *fakeUpstreamSession) Data(r io.Reader) error {
	if _, err := io.ReadAll(r); err != nil {
		return err
	}
	if s.opts.HangOnData {
		<-s.opts.ctx.Done()
		return context.Canceled
	}
	return nil
}

func (s *fakeUpstreamSession) Reset()        {}
func (s *fakeUpstreamSession) Logout() error { return nil }

// fakeUpstream is a handle to a throwaway SMTP server standing in for a
// real upstream provider in tests.
type fakeUpstream struct {
	Addr      string
	ClientTLS *tls.Config
}

// startFakeUpstream starts a loopback SMTP server advertising STARTTLS with
// an in-process self-signed certificate, per opts. The server and its
// listener are torn down automatically via t.Cleanup.
func startFakeUpstream(t *testing.T, opts fakeUpstreamOptions) fakeUpstream {
	t.Helper()

	opts.ctx = t.Context()
	serverTLS, clientTLS, _ := generateSelfSignedCert(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := smtp.NewServer(&fakeUpstreamBackend{opts: opts})
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	srv.TLSConfig = serverTLS

	go func() {
		_ = srv.Serve(l)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return fakeUpstream{Addr: l.Addr().String(), ClientTLS: clientTLS}
}

// unreachableAddr returns an address with nothing listening on it, for
// exercising dial-failure paths. It binds a listener and immediately closes
// it, so the port is real but refuses connections.
func unreachableAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}
	return addr
}
