package mockmt

import (
	"bytes"
	"encoding/pem"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearRelayEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"RELAY_ENABLED", "RELAY_HOST", "RELAY_PORT", "RELAY_USERNAME", "RELAY_PASSWORD",
		"RELAY_TLS_MODE", "RELAY_IDENTITY", "RELAY_TIMEOUT_SECONDS", "RELAY_CA_CERT_FILE",
		"REVIEWER_EMAILS", "MAX_MESSAGE_BYTES", "SMTP_MAX_CONCURRENT",
		"SMTP_READ_TIMEOUT_SECONDS", "SMTP_WRITE_TIMEOUT_SECONDS", "RELAY_MAX_CONCURRENT_IO",
		"RETENTION_DAYS",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestLoadRelayConfigDisabledByDefault(t *testing.T) {
	clearRelayEnv(t)

	cfg, err := LoadRelayConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("expected relay to be disabled by default")
	}
}

func TestLoadRelayConfigDefaults(t *testing.T) {
	clearRelayEnv(t)

	cfg, err := LoadRelayConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "587" {
		t.Errorf("Port = %q, want 587", cfg.Port)
	}
	if cfg.TLSMode != "starttls" {
		t.Errorf("TLSMode = %q, want starttls", cfg.TLSMode)
	}
	if cfg.TimeoutSeconds != 10 {
		t.Errorf("TimeoutSeconds = %d, want 10", cfg.TimeoutSeconds)
	}
	if cfg.MaxMessageBytes != 26214400 {
		t.Errorf("MaxMessageBytes = %d, want 26214400", cfg.MaxMessageBytes)
	}
	if cfg.SMTPMaxConcurrent != 3 {
		t.Errorf("SMTPMaxConcurrent = %d, want 3", cfg.SMTPMaxConcurrent)
	}
	if cfg.RelayMaxConcurrentIO != 2 {
		t.Errorf("RelayMaxConcurrentIO = %d, want 2", cfg.RelayMaxConcurrentIO)
	}
	if cfg.SMTPReadTimeoutSeconds != 60 || cfg.SMTPWriteTimeoutSeconds != 60 {
		t.Errorf("read/write timeouts = %d/%d, want 60/60", cfg.SMTPReadTimeoutSeconds, cfg.SMTPWriteTimeoutSeconds)
	}
	if cfg.RetentionDays != 0 {
		t.Errorf("RetentionDays = %d, want 0", cfg.RetentionDays)
	}
}

// FR-004: every missing upstream setting is reported together, not just
// the first one encountered.
func TestLoadRelayConfigReportsAllMissingSettings(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")

	_, err := LoadRelayConfig()
	if err == nil {
		t.Fatal("expected an error when relay is enabled with no other settings")
	}

	for _, want := range []string{"RELAY_HOST", "RELAY_USERNAME", "RELAY_PASSWORD", "RELAY_IDENTITY", "REVIEWER_EMAILS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention missing setting %s", err.Error(), want)
		}
	}
}

// FR-017b: relay mode must refuse to start with an empty reviewer list even
// when every other setting is present, because nobody could release
// queued mail.
func TestLoadRelayConfigFailsOnEmptyReviewerList(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_HOST", "smtp.example.com")
	t.Setenv("RELAY_USERNAME", "user")
	t.Setenv("RELAY_PASSWORD", "pass")
	t.Setenv("RELAY_IDENTITY", "relay@example.com")
	t.Setenv("REVIEWER_EMAILS", "")

	_, err := LoadRelayConfig()
	if err == nil {
		t.Fatal("expected an error for an empty reviewer list")
	}
	if !strings.Contains(err.Error(), "REVIEWER_EMAILS") {
		t.Errorf("error %q does not mention REVIEWER_EMAILS", err.Error())
	}
}

func TestLoadRelayConfigSucceedsWithCompleteSettings(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_HOST", "smtp.example.com")
	t.Setenv("RELAY_USERNAME", "user")
	t.Setenv("RELAY_PASSWORD", "pass")
	t.Setenv("RELAY_IDENTITY", "relay@example.com")
	t.Setenv("REVIEWER_EMAILS", "alice@example.com,bob@example.com")

	cfg, err := LoadRelayConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected relay to be enabled")
	}
	if cfg.ReviewerCount() != 2 {
		t.Fatalf("ReviewerCount() = %d, want 2", cfg.ReviewerCount())
	}
}

func TestLoadRelayConfigRejectsInvalidTLSMode(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_HOST", "smtp.example.com")
	t.Setenv("RELAY_USERNAME", "user")
	t.Setenv("RELAY_PASSWORD", "pass")
	t.Setenv("RELAY_IDENTITY", "relay@example.com")
	t.Setenv("REVIEWER_EMAILS", "alice@example.com")
	t.Setenv("RELAY_TLS_MODE", "plaintext")

	_, err := LoadRelayConfig()
	if err == nil {
		t.Fatal("expected an error for an invalid TLS mode")
	}
	if !strings.Contains(err.Error(), "RELAY_TLS_MODE") {
		t.Errorf("error %q does not mention RELAY_TLS_MODE", err.Error())
	}
}

func TestLoadRelayConfigRejectsInvalidNumericSetting(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("SMTP_MAX_CONCURRENT", "not-a-number")

	_, err := LoadRelayConfig()
	if err == nil {
		t.Fatal("expected an error for a malformed numeric setting")
	}
	if !strings.Contains(err.Error(), "SMTP_MAX_CONCURRENT") {
		t.Errorf("error %q does not mention SMTP_MAX_CONCURRENT", err.Error())
	}
}

func TestIsReviewerMatchesCaseInsensitively(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_HOST", "smtp.example.com")
	t.Setenv("RELAY_USERNAME", "user")
	t.Setenv("RELAY_PASSWORD", "pass")
	t.Setenv("RELAY_IDENTITY", "relay@example.com")
	t.Setenv("REVIEWER_EMAILS", " Alice@Example.com , bob@example.com")

	cfg, err := LoadRelayConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, addr := range []string{"alice@example.com", "ALICE@EXAMPLE.COM", "bob@example.com"} {
		if !cfg.IsReviewer(addr) {
			t.Errorf("IsReviewer(%q) = false, want true", addr)
		}
	}
	if cfg.IsReviewer("carol@example.com") {
		t.Error("IsReviewer(carol@example.com) = true, want false")
	}
}

func TestLoadRelayConfigLoadsCACertFile(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_HOST", "smtp.example.com")
	t.Setenv("RELAY_USERNAME", "user")
	t.Setenv("RELAY_PASSWORD", "pass")
	t.Setenv("RELAY_IDENTITY", "relay@example.com")
	t.Setenv("REVIEWER_EMAILS", "alice@example.com")

	_, _, leaf := generateSelfSignedCert(t)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}
	t.Setenv("RELAY_CA_CERT_FILE", caFile)

	cfg, err := LoadRelayConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TLSConfig == nil {
		t.Fatal("expected TLSConfig to be populated from RELAY_CA_CERT_FILE")
	}
}

func TestLoadRelayConfigReportsUnreadableCACertFile(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_HOST", "smtp.example.com")
	t.Setenv("RELAY_USERNAME", "user")
	t.Setenv("RELAY_PASSWORD", "pass")
	t.Setenv("RELAY_IDENTITY", "relay@example.com")
	t.Setenv("REVIEWER_EMAILS", "alice@example.com")
	t.Setenv("RELAY_CA_CERT_FILE", filepath.Join(t.TempDir(), "does-not-exist.pem"))

	_, err := LoadRelayConfig()
	if err == nil {
		t.Fatal("expected an error for an unreadable CA cert file")
	}
	if !strings.Contains(err.Error(), "RELAY_CA_CERT_FILE") {
		t.Errorf("error %q does not mention RELAY_CA_CERT_FILE", err.Error())
	}
}

// SC-009: no credential value should ever appear in a config error string.
func TestLoadRelayConfigErrorsNeverContainCredentials(t *testing.T) {
	clearRelayEnv(t)
	t.Setenv("RELAY_ENABLED", "true")
	t.Setenv("RELAY_PASSWORD", "super-secret-password")

	_, err := LoadRelayConfig()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "super-secret-password") {
		t.Fatalf("error message leaked the configured password: %v", err)
	}
}

// SC-009: the startup summary log line must never contain the configured
// credentials, even though it does report host, port, and identity.
func TestLogRelayStartupSummaryNeverLogsCredentials(t *testing.T) {
	cfg := &RelayConfig{
		Enabled:       true,
		Host:          "smtp.example.com",
		Port:          "587",
		Username:      "relay-user",
		Password:      "super-secret-password",
		TLSMode:       "starttls",
		Identity:      "relay@example.com",
		RetentionDays: 0,
		reviewers:     map[string]struct{}{"alice@example.com": {}},
	}

	var buf bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalOutput)

	LogRelayStartupSummary(cfg)

	out := buf.String()
	if strings.Contains(out, "super-secret-password") || strings.Contains(out, "relay-user") {
		t.Fatalf("startup log leaked credentials: %s", out)
	}
	if !strings.Contains(out, "smtp.example.com") {
		t.Errorf("expected startup log to mention the upstream host, got: %s", out)
	}
}
