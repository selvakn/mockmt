// Command fakeupstream is a disposable SMTP server standing in for a real
// upstream mail provider during local end-to-end testing of
// relay-with-approval mode. It exists because mockmt's own capture-only
// SMTP server never advertises STARTTLS (research R1) and the relay
// client requires TLS unconditionally. It accepts any AUTH PLAIN
// credentials, reads DATA to completion, and logs one line per message
// received -- this log is the independent evidence (outside the
// application's own UI) that a relayed message actually arrived. See
// specs/003-e2e-docker-testrig/contracts/fake-upstream.md for the full
// contract.
package main

import (
	"crypto/tls"
	"io"
	"log"
	"os"
	"strings"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

type backend struct{}

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &session{}, nil
}

type session struct {
	from string
	to   []string
}

func (s *session) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *session) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		// A delivery double, not an authentication boundary: this server
		// exists to prove a relayed message arrives over a genuinely
		// verified TLS connection, not to test credential checking --
		// that is already covered by the application's own inbound
		// AUTH PLAIN (feature 001/002).
		return nil
	}), nil
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	log.Printf("received message: from=%s to=%v subject=%q size=%d bytes", s.from, s.to, extractSubject(raw), len(raw))
	return nil
}

// extractSubject does a minimal header-block scan for a Subject line,
// deliberately avoiding a full MIME parser: this program only needs
// enough to log something recognizable, not to render the message.
func extractSubject(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(strings.ToLower(line), "subject:") {
			return strings.TrimSpace(line[len("subject:"):])
		}
	}
	return "(no subject found)"
}

func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error { return nil }

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	certFile := getEnv("FAKE_UPSTREAM_CERT_FILE", "/certs/fake-upstream-cert.pem")
	keyFile := getEnv("FAKE_UPSTREAM_KEY_FILE", "/certs/fake-upstream-key.pem")
	port := getEnv("FAKE_UPSTREAM_PORT", "587")

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("failed to load certificate: %v", err)
	}

	s := smtp.NewServer(&backend{})
	s.Addr = ":" + port
	s.Domain = "fake-upstream"
	s.AllowInsecureAuth = true
	s.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}

	log.Printf("fake-upstream listening on %s (STARTTLS available)", s.Addr)
	log.Fatal(s.ListenAndServe())
}
