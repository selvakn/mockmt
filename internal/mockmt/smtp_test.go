package mockmt

import (
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

// US3: operator-configured SMTP credentials via environment variables.

func TestLoadSMTPCredentials(t *testing.T) {
	t.Run("missing username", func(t *testing.T) {
		t.Setenv("SMTP_USERNAME", "")
		t.Setenv("SMTP_PASSWORD", "pass")
		if _, _, err := loadSMTPCredentials(); err == nil {
			t.Fatal("expected error when SMTP_USERNAME is unset")
		}
	})

	t.Run("missing password", func(t *testing.T) {
		t.Setenv("SMTP_USERNAME", "user")
		t.Setenv("SMTP_PASSWORD", "")
		if _, _, err := loadSMTPCredentials(); err == nil {
			t.Fatal("expected error when SMTP_PASSWORD is unset")
		}
	})

	t.Run("both set", func(t *testing.T) {
		t.Setenv("SMTP_USERNAME", "user")
		t.Setenv("SMTP_PASSWORD", "pass")
		username, password, err := loadSMTPCredentials()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if username != "user" || password != "pass" {
			t.Fatalf("got (%q, %q), want (user, pass)", username, password)
		}
	})
}
