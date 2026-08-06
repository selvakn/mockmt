package mockmt

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// RelayConfig holds the operator-supplied settings for relay-with-approval
// mode (FR-003) plus the SMTP intake limits that apply regardless of mode
// (research R16). Never persisted and never exposed through the API
// (FR-006).
type RelayConfig struct {
	Enabled bool

	Host     string
	Port     string
	Username string
	Password string
	TLSMode  string // "starttls" | "tls" -- no plaintext option (FR-029)
	Identity string

	TimeoutSeconds int

	// TLSConfig is nil for system roots. Tests inject a config trusting a
	// self-signed root so they exercise the same TLS path as production
	// (research R18). There is deliberately no option to skip
	// verification.
	TLSConfig *tls.Config

	// reviewers holds lower-cased, trimmed reviewer email addresses.
	reviewers map[string]struct{}

	MaxMessageBytes         int64
	SMTPMaxConcurrent       int
	SMTPReadTimeoutSeconds  int
	SMTPWriteTimeoutSeconds int
	RelayMaxConcurrentIO    int

	RetentionDays int
}

// IsReviewer reports whether email is on the operator-configured reviewer
// list, matched case-insensitively (FR-017a). Resolved fresh on every call
// rather than cached in a token, so removing a reviewer takes effect on
// their very next request rather than when a 24-hour JWT expires.
func (c *RelayConfig) IsReviewer(email string) bool {
	if c == nil {
		return false
	}
	_, ok := c.reviewers[normalizeReviewerEmail(email)]
	return ok
}

// ReviewerCount reports how many reviewer addresses are configured, for
// startup logging (FR-005) -- never the addresses themselves.
func (c *RelayConfig) ReviewerCount() int {
	if c == nil {
		return 0
	}
	return len(c.reviewers)
}

func normalizeReviewerEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func parseReviewerList(raw string) map[string]struct{} {
	reviewers := make(map[string]struct{})
	for _, addr := range strings.Split(raw, ",") {
		addr = normalizeReviewerEmail(addr)
		if addr == "" {
			continue
		}
		reviewers[addr] = struct{}{}
	}
	return reviewers
}

// LoadRelayConfig reads relay mode and intake-limit configuration from the
// environment. When relay mode is enabled, every missing or invalid
// setting is reported together in one error (FR-004), rather than the
// operator fixing one, restarting, and discovering the next.
func LoadRelayConfig() (*RelayConfig, error) {
	var problems []string

	cfg := &RelayConfig{
		Enabled:  getEnv("RELAY_ENABLED", "false") == "true",
		Host:     getEnv("RELAY_HOST", ""),
		Port:     getEnv("RELAY_PORT", "587"),
		Username: getEnv("RELAY_USERNAME", ""),
		Password: getEnv("RELAY_PASSWORD", ""),
		TLSMode:  getEnv("RELAY_TLS_MODE", "starttls"),
		Identity: getEnv("RELAY_IDENTITY", ""),
	}

	cfg.TimeoutSeconds = parseIntSetting("RELAY_TIMEOUT_SECONDS", 10, &problems)
	cfg.MaxMessageBytes = parseInt64Setting("MAX_MESSAGE_BYTES", 26214400, &problems)
	cfg.SMTPMaxConcurrent = parseIntSetting("SMTP_MAX_CONCURRENT", 3, &problems)
	cfg.SMTPReadTimeoutSeconds = parseIntSetting("SMTP_READ_TIMEOUT_SECONDS", 60, &problems)
	cfg.SMTPWriteTimeoutSeconds = parseIntSetting("SMTP_WRITE_TIMEOUT_SECONDS", 60, &problems)
	cfg.RelayMaxConcurrentIO = parseIntSetting("RELAY_MAX_CONCURRENT_IO", 2, &problems)
	cfg.RetentionDays = parseIntSetting("RETENTION_DAYS", 0, &problems)

	cfg.reviewers = parseReviewerList(getEnv("REVIEWER_EMAILS", ""))

	if !cfg.Enabled {
		if len(problems) > 0 {
			return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
		}
		return cfg, nil
	}

	if cfg.Host == "" {
		problems = append(problems, "RELAY_HOST is not set")
	}
	if cfg.Username == "" {
		problems = append(problems, "RELAY_USERNAME is not set")
	}
	if cfg.Password == "" {
		problems = append(problems, "RELAY_PASSWORD is not set")
	}
	if cfg.Identity == "" {
		problems = append(problems, "RELAY_IDENTITY is not set")
	}
	if cfg.TLSMode != "starttls" && cfg.TLSMode != "tls" {
		problems = append(problems, fmt.Sprintf("RELAY_TLS_MODE must be \"starttls\" or \"tls\", got %q", cfg.TLSMode))
	}
	if len(cfg.reviewers) == 0 {
		problems = append(problems, "REVIEWER_EMAILS is empty (nobody could release queued mail)")
	}

	if caFile := getEnv("RELAY_CA_CERT_FILE", ""); caFile != "" {
		tlsConfig, err := loadRelayTLSConfig(caFile)
		if err != nil {
			problems = append(problems, fmt.Sprintf("RELAY_CA_CERT_FILE: %v", err))
		} else {
			cfg.TLSConfig = tlsConfig
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("relay mode is enabled but misconfigured:\n  - %s", strings.Join(problems, "\n  - "))
	}

	return cfg, nil
}

// loadRelayTLSConfig builds a TLS config trusting the system roots plus the
// certificate at caFile. There is deliberately no option to skip
// verification (research R18) -- an operator relaying through a
// self-hosted upstream with a private CA should point here instead.
func loadRelayTLSConfig(caFile string) (*tls.Config, error) {
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate file: %w", err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("no valid certificates found in %s", caFile)
	}

	return &tls.Config{RootCAs: pool}, nil
}

// relayCfg holds the currently active relay configuration, set once at
// startup by InitRelay. Package-level by the same convention as db (see
// database.go) and oauthConfig/jwtSecret (see auth.go).
var relayCfg *RelayConfig

// relayIOSem bounds concurrent whole-message reads across the relay HTTP
// API (research R16); set by InitRelay alongside relayCfg.
var relayIOSem *ioSemaphore

// InitRelay wires a loaded RelayConfig into the running process. Called
// once at startup, after LoadRelayConfig succeeds. requireReviewer(), the
// relay HTTP handlers, and the SMTP ingest path all consult relayCfg to
// decide whether relay mode is active and who may act as a reviewer.
func InitRelay(cfg *RelayConfig) {
	relayCfg = cfg
	relayIOSem = newIOSemaphore(cfg.RelayMaxConcurrentIO)
}

// LogRelayStartupSummary logs the active mode and its settings once at
// startup (FR-005), so an operator can always tell whether an instance can
// relay without reading its environment file. Credentials are never
// logged (FR-006) -- only host, port, identity, reviewer count, and
// retention setting.
func LogRelayStartupSummary(cfg *RelayConfig) {
	if cfg == nil || !cfg.Enabled {
		log.Println("Relay mode: DISABLED (capture-only)")
		return
	}

	retention := "disabled"
	if cfg.RetentionDays > 0 {
		retention = fmt.Sprintf("%d days", cfg.RetentionDays)
	}

	log.Printf("Relay mode: ENABLED (upstream %s:%s, %s, identity %s)", cfg.Host, cfg.Port, cfg.TLSMode, cfg.Identity)
	log.Printf("Reviewers: %d configured", cfg.ReviewerCount())
	log.Printf("Retention: %s", retention)
}

func parseIntSetting(key string, def int, problems *[]string) int {
	raw := getEnv(key, strconv.Itoa(def))
	v, err := strconv.Atoi(raw)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s must be an integer, got %q", key, raw))
		return def
	}
	return v
}

func parseInt64Setting(key string, def int64, problems *[]string) int64 {
	raw := getEnv(key, strconv.FormatInt(def, 10))
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s must be an integer, got %q", key, raw))
		return def
	}
	return v
}
