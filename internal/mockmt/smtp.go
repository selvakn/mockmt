package mockmt

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

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

func (s *Session) Data(r io.Reader) error {
	mr, err := mail.CreateReader(r)
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

	log.Printf("Starting SMTP server at %s", s.Addr)
	return s.ListenAndServe()
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
