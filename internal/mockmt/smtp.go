package mockmt

import (
	"errors"
	"io"
	"log"
	"strings"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-smtp"
)

var ErrInvalidAddress = errors.New("invalid address")

type Backend struct{}

func (bkd *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &Session{}, nil
}

type Session struct {
	from string
	to   []string
}

func (s *Session) AuthPlain(username, password string) error {
	return nil // No authentication required
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
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

func StartSMTPServer() error {
	be := &Backend{}

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
