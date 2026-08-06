package mockmt

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

var ErrInvalidAddress = errors.New("invalid address")

type Backend struct {
	Username string
	Password string
}

func (bkd *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &Session{backend: bkd}, nil
}

type Session struct {
	backend       *Backend
	authenticated bool
	from          string
	to            []string
}

func (s *Session) AuthMechanisms() []string {
	return []string{sasl.Plain}
}

func (s *Session) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		validUsername := subtle.ConstantTimeCompare([]byte(username), []byte(s.backend.Username))
		validPassword := subtle.ConstantTimeCompare([]byte(password), []byte(s.backend.Password))
		if validUsername&validPassword != 1 {
			log.Printf("SMTP auth failed: user=%q", username)
			return smtp.ErrAuthFailed
		}
		log.Printf("SMTP auth succeeded: user=%q", username)
		s.authenticated = true
		return nil
	}), nil
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	if !s.authenticated {
		return smtp.ErrAuthRequired
	}
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	// if !strings.HasSuffix(to, "@localhost") {
	// 	return &smtp.SMTPError{
	// 		Code:    550,
	// 		Message: "Invalid address",
	// 	}
	// }
	s.to = append(s.to, to)
	return nil
}

// Data reads the complete message into memory before doing anything else,
// then branches on the operating mode. Reading raw bytes first is what
// makes relay mode possible at all (FR-008): the previous implementation
// consumed the stream through mail.CreateReader and kept only the parsed
// subject/text/html, discarding attachments and every other header, which
// cannot be relayed faithfully. Capture-only mode's own parsing is
// unchanged -- it now runs against a buffer instead of the live stream,
// but produces identical results (FR-002).
func (s *Session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		log.Printf("Error reading message data: %v", err)
		return err
	}

	if relayCfg != nil && relayCfg.Enabled {
		return s.queueForReview(raw)
	}
	return s.captureMessage(raw)
}

// captureMessage is today's capture-only behaviour, byte-for-byte: parse
// subject/text/html and store once per recipient via saveEmail.
func (s *Session) captureMessage(raw []byte) error {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		log.Printf("Error creating mail reader: %v", err)
		return err
	}

	header := mr.Header
	subject := header.Get("Subject")
	if subject == "" {
		subject = "No Subject"
	}

	body := ""
	htmlBody := ""

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Printf("Error reading part: %v", err)
			break
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			if strings.HasPrefix(contentType, "text/plain") {
				b, _ := io.ReadAll(p.Body)
				body = string(b)
			} else if strings.HasPrefix(contentType, "text/html") {
				b, _ := io.ReadAll(p.Body)
				htmlBody = string(b)
			}
		case *mail.AttachmentHeader:
			// We can handle attachments here in the future
		}
	}

	if body == "" && htmlBody != "" {
		body = stripHTML(htmlBody)
	}

	for _, to := range s.to {
		if err := saveEmail(s.from, to, subject, body, htmlBody); err != nil {
			log.Printf("Error saving email: %v", err)
			return err
		}
		log.Printf("Email saved: from=%s, to=%s, subject=%s", s.from, to, subject)
	}

	return nil
}

// queueForReview is relay-mode ingest (FR-007): the message is stored
// complete and durably, and is acknowledged to the client only once that
// commit succeeds (FR-009). It is never delivered here -- that happens
// only when a reviewer presses Send Now.
func (s *Session) queueForReview(raw []byte) error {
	meta, err := parseMessageMetadata(raw)
	if err != nil {
		log.Printf("Error parsing message metadata: %v", err)
		return err
	}

	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		log.Printf("Error parsing message header: %v", err)
		return err
	}

	headerFrom := ""
	if from, ferr := mr.Header.AddressList("From"); ferr == nil && len(from) > 0 {
		headerFrom = from[0].String()
	}

	hidden := hiddenRecipients(&mr.Header, s.to)
	recipients := make([]queuedRecipientInput, len(s.to))
	for i, to := range s.to {
		recipients[i] = queuedRecipientInput{Address: to, Hidden: hidden[to]}
	}

	id, err := insertQueuedMessage(s.from, headerFrom, meta.Subject, raw, recipients)
	if err != nil {
		log.Printf("Error queueing message for review: %v", err)
		return err
	}

	log.Printf("Message queued for review: id=%d, from=%s, recipients=%d", id, s.from, len(s.to))
	return nil
}

func (s *Session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *Session) Logout() error {
	return nil
}

// LoadSMTPCredentials reads the required SMTP_USERNAME/SMTP_PASSWORD
// configuration. Callers should treat a non-nil error as fatal.
func LoadSMTPCredentials() (string, string, error) {
	username := getEnv("SMTP_USERNAME", "")
	password := getEnv("SMTP_PASSWORD", "")

	if username == "" {
		return "", "", fmt.Errorf("SMTP_USERNAME is not set")
	}
	if password == "" {
		return "", "", fmt.Errorf("SMTP_PASSWORD is not set")
	}

	return username, password, nil
}

// Defaults applied when StartSMTPServer runs without InitRelay having been
// called first. Production always calls InitRelay before starting the
// SMTP server (main.go); these mirror LoadRelayConfig's own defaults so a
// missing call fails safe rather than unlimited.
const (
	defaultSMTPMaxConcurrent       = 3
	defaultMaxMessageBytes   int64 = 26214400
	defaultSMTPReadTimeout         = 60 * time.Second
	defaultSMTPWriteTimeout        = 60 * time.Second
)

func StartSMTPServer() error {
	username, password, err := LoadSMTPCredentials()
	if err != nil {
		return fmt.Errorf("SMTP authentication is not configured: %w", err)
	}

	be := &Backend{Username: username, Password: password}

	s := smtp.NewServer(be)

	smtpPort := getEnv("SMTP_PORT", "25")
	s.Addr = ":" + smtpPort
	s.Domain = "localhost"
	s.AllowInsecureAuth = true

	maxConns := defaultSMTPMaxConcurrent
	s.MaxMessageBytes = defaultMaxMessageBytes
	s.ReadTimeout = defaultSMTPReadTimeout
	s.WriteTimeout = defaultSMTPWriteTimeout
	// MaxRecipients is deliberately left at its zero value (unlimited),
	// matching today's behaviour -- no requirement calls for a specific
	// recipient cap, and go-smtp treats 0 as "no limit".

	if relayCfg != nil {
		if relayCfg.SMTPMaxConcurrent > 0 {
			maxConns = relayCfg.SMTPMaxConcurrent
		}
		s.MaxMessageBytes = relayCfg.MaxMessageBytes
		s.ReadTimeout = time.Duration(relayCfg.SMTPReadTimeoutSeconds) * time.Second
		s.WriteTimeout = time.Duration(relayCfg.SMTPWriteTimeoutSeconds) * time.Second
	}

	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}

	// The connection cap without the read/write timeouts above would be a
	// regression, not an improvement: go-smtp applies no idle timeout by
	// default, so a handful of idle connections would occupy every slot
	// and block all submission indefinitely (research R16).
	log.Printf("Starting SMTP server at %s (max %d concurrent connections)", s.Addr, maxConns)
	return s.Serve(newLimitedListener(l, maxConns))
}

func stripHTML(html string) string {
	// Simple HTML stripping - remove tags
	result := html
	result = strings.ReplaceAll(result, "<br>", "\n")
	result = strings.ReplaceAll(result, "<br/>", "\n")
	result = strings.ReplaceAll(result, "<br />", "\n")

	for {
		start := strings.Index(result, "<")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end == -1 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}

	return strings.TrimSpace(result)
}
