package mockmt

import (
	"net"
	netsmtp "net/smtp"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", filepath.Join(t.TempDir(), "test.db"))
	if err := InitDatabase(); err != nil {
		t.Fatalf("failed to init test database: %v", err)
	}
}

func countEmails(t *testing.T) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM emails").Scan(&count); err != nil {
		t.Fatalf("failed to count emails: %v", err)
	}
	return count
}

func countQueuedMessages(t *testing.T) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM queued_messages").Scan(&count); err != nil {
		t.Fatalf("failed to count queued messages: %v", err)
	}
	return count
}

func countUsers(t *testing.T) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("failed to count users: %v", err)
	}
	return count
}

func authenticate(t *testing.T, s *Session, username, password string) {
	t.Helper()
	srv, err := s.Auth(sasl.Plain)
	if err != nil {
		t.Fatalf("Auth returned error: %v", err)
	}
	_, ir, err := sasl.NewPlainClient("", username, password).Start()
	if err != nil {
		t.Fatalf("failed to build PLAIN initial response: %v", err)
	}
	if _, _, err := srv.Next(ir); err != nil {
		t.Fatalf("authentication failed: %v", err)
	}
	if !s.authenticated {
		t.Fatal("expected session to be authenticated")
	}
}

func sendTestMessage(t *testing.T, s *Session) {
	t.Helper()
	if err := s.Mail("sender@example.com", &smtp.MailOptions{}); err != nil {
		t.Fatalf("Mail failed: %v", err)
	}
	if err := s.Rcpt("recipient@localhost", &smtp.RcptOptions{}); err != nil {
		t.Fatalf("Rcpt failed: %v", err)
	}
	if err := s.Data(strings.NewReader("Subject: Hello\r\n\r\nHello world\r\n")); err != nil {
		t.Fatalf("Data failed: %v", err)
	}
}

// US1: reject mail from unauthenticated senders.

func TestMailRejectsUnauthenticatedSession(t *testing.T) {
	setupTestDB(t)

	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}

	if err := s.Mail("sender@example.com", &smtp.MailOptions{}); err != smtp.ErrAuthRequired {
		t.Fatalf("expected ErrAuthRequired, got %v", err)
	}
	if got := countEmails(t); got != 0 {
		t.Fatalf("expected no emails stored, got %d", got)
	}
}

func TestAuthPlainRejectsIncorrectCredentials(t *testing.T) {
	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}

	srv, err := s.Auth(sasl.Plain)
	if err != nil {
		t.Fatalf("Auth returned error: %v", err)
	}

	_, ir, err := sasl.NewPlainClient("", "user", "wrong-password").Start()
	if err != nil {
		t.Fatalf("failed to build PLAIN initial response: %v", err)
	}

	if _, _, err := srv.Next(ir); err == nil {
		t.Fatal("expected error for incorrect credentials, got nil")
	}
	if s.authenticated {
		t.Fatal("expected session to remain unauthenticated after failed auth")
	}
}

// US2: authenticated senders keep working, including multiple messages per connection.

func TestAuthenticatedSessionCanSendMail(t *testing.T) {
	setupTestDB(t)

	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}
	authenticate(t, s, "user", "pass")
	sendTestMessage(t, s)

	if got := countEmails(t); got != 1 {
		t.Fatalf("expected 1 email stored, got %d", got)
	}
}

func TestAuthenticatedSessionCanSendMultipleMessages(t *testing.T) {
	setupTestDB(t)

	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}
	authenticate(t, s, "user", "pass")

	sendTestMessage(t, s)
	s.Reset()
	sendTestMessage(t, s)

	if got := countEmails(t); got != 2 {
		t.Fatalf("expected 2 emails stored, got %d", got)
	}
	if !s.authenticated {
		t.Fatal("expected session to remain authenticated across Reset()")
	}
}

func TestSessionAdvertisesPlainMechanismOnly(t *testing.T) {
	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}

	got := s.AuthMechanisms()
	if len(got) != 1 || got[0] != sasl.Plain {
		t.Fatalf("got %v, want [%s]", got, sasl.Plain)
	}
}

// Wire-protocol integration tests: drive a real Backend through go-smtp's
// Server/Serve against the standard library's net/smtp client, so the
// AuthSession wiring (capability advertisement + response codes) is
// exercised end-to-end rather than only through direct method calls.

func startTestSMTPServer(t *testing.T, username, password string) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := smtp.NewServer(&Backend{Username: username, Password: password})
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true

	go func() {
		_ = srv.Serve(l)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return l.Addr().String()
}

func TestWireProtocolAdvertisesAuthAndRejectsUnauthenticatedMail(t *testing.T) {
	setupTestDB(t)
	addr := startTestSMTPServer(t, "devbox", "s3cr3t")

	c, err := netsmtp.Dial(addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello("localhost"); err != nil {
		t.Fatalf("EHLO failed: %v", err)
	}

	ok, params := c.Extension("AUTH")
	if !ok || !strings.Contains(params, "PLAIN") {
		t.Fatalf("expected server to advertise AUTH PLAIN, got ok=%v params=%q", ok, params)
	}

	err = c.Mail("sender@example.com")
	if err == nil {
		t.Fatal("expected MAIL FROM to be rejected before authentication")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected a 502 response, got: %v", err)
	}
	if got := countEmails(t); got != 0 {
		t.Fatalf("expected no emails stored, got %d", got)
	}
}

func TestWireProtocolRejectsBadCredentials(t *testing.T) {
	addr := startTestSMTPServer(t, "devbox", "s3cr3t")

	c, err := netsmtp.Dial(addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello("localhost"); err != nil {
		t.Fatalf("EHLO failed: %v", err)
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("failed to split addr: %v", err)
	}

	auth := netsmtp.PlainAuth("", "devbox", "wrong-password", host)
	err = c.Auth(auth)
	if err == nil {
		t.Fatal("expected authentication with the wrong password to fail")
	}
	if !strings.Contains(err.Error(), "535") {
		t.Fatalf("expected a 535 response, got: %v", err)
	}
}

func TestWireProtocolAcceptsAuthenticatedMail(t *testing.T) {
	setupTestDB(t)
	addr := startTestSMTPServer(t, "devbox", "s3cr3t")

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("failed to split addr: %v", err)
	}

	auth := netsmtp.PlainAuth("", "devbox", "s3cr3t", host)
	msg := []byte("Subject: Hello\r\n\r\nHello world\r\n")
	if err := netsmtp.SendMail(addr, auth, "sender@example.com", []string{"recipient@localhost"}, msg); err != nil {
		t.Fatalf("SendMail failed: %v", err)
	}

	if got := countEmails(t); got != 1 {
		t.Fatalf("expected 1 email stored, got %d", got)
	}
}

// US2 (capture-only regression, SC-001): with relay mode disabled, ingest
// must behave exactly as it does today -- write to emails, create the
// recipient's user account, and write nothing to the relay tables.
func TestCaptureOnlyModeUnaffectedByRelaySchema(t *testing.T) {
	setupTestDB(t)
	setRelayConfigForTest(t, &RelayConfig{Enabled: false})

	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}
	authenticate(t, s, "user", "pass")
	sendTestMessage(t, s)

	if got := countEmails(t); got != 1 {
		t.Fatalf("expected 1 email stored in emails, got %d", got)
	}
	if got := countUsers(t); got != 1 {
		t.Fatalf("expected 1 user auto-created for the recipient, got %d", got)
	}
	if got := countQueuedMessages(t); got != 0 {
		t.Fatalf("expected 0 rows in queued_messages with relay disabled, got %d", got)
	}
}

// US1: with relay mode enabled, ingest queues the complete raw message
// for review instead of capturing it, and creates no portal user for the
// recipient (FR-007, FR-018a).
func TestRelayModeQueuesInsteadOfCapturing(t *testing.T) {
	setupTestDB(t)
	setRelayConfigForTest(t, &RelayConfig{Enabled: true})

	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}
	authenticate(t, s, "user", "pass")

	if err := s.Mail("agent@myapp.local", &smtp.MailOptions{}); err != nil {
		t.Fatalf("Mail failed: %v", err)
	}
	if err := s.Rcpt("customer@example.com", &smtp.RcptOptions{}); err != nil {
		t.Fatalf("Rcpt failed: %v", err)
	}
	if err := s.Data(strings.NewReader("Subject: Hello\r\nFrom: agent@myapp.local\r\nTo: customer@example.com\r\n\r\nHello world\r\n")); err != nil {
		t.Fatalf("Data failed: %v", err)
	}

	if got := countEmails(t); got != 0 {
		t.Fatalf("expected 0 rows in emails with relay enabled, got %d", got)
	}
	if got := countQueuedMessages(t); got != 1 {
		t.Fatalf("expected 1 queued message, got %d", got)
	}
	if got := countUsers(t); got != 0 {
		t.Fatalf("expected 0 users created for the external recipient, got %d", got)
	}
}

// US3: operator-configured SMTP credentials via environment variables.

func TestLoadSMTPCredentials(t *testing.T) {
	t.Run("missing username", func(t *testing.T) {
		t.Setenv("SMTP_USERNAME", "")
		t.Setenv("SMTP_PASSWORD", "pass")
		if _, _, err := LoadSMTPCredentials(); err == nil {
			t.Fatal("expected error when SMTP_USERNAME is unset")
		}
	})

	t.Run("missing password", func(t *testing.T) {
		t.Setenv("SMTP_USERNAME", "user")
		t.Setenv("SMTP_PASSWORD", "")
		if _, _, err := LoadSMTPCredentials(); err == nil {
			t.Fatal("expected error when SMTP_PASSWORD is unset")
		}
	})

	t.Run("both set", func(t *testing.T) {
		t.Setenv("SMTP_USERNAME", "user")
		t.Setenv("SMTP_PASSWORD", "pass")
		username, password, err := LoadSMTPCredentials()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if username != "user" || password != "pass" {
			t.Fatalf("got (%q, %q), want (user, pass)", username, password)
		}
	})
}
