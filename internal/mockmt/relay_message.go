package mockmt

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
)

// attachmentMetadata describes one attachment without holding its bytes.
type attachmentMetadata struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
}

// messageMetadata is the display-oriented view of a queued message,
// derived by parsing a copy of its raw bytes (research R11) -- never
// stored, since the raw message already holds everything needed to
// reconstruct it (FR-016).
type messageMetadata struct {
	Subject     string
	TextBody    string
	HTMLBody    string
	Attachments []attachmentMetadata
}

// parseMessageMetadata extracts the subject, plain-text body, HTML body,
// and attachment list (filename, declared type, size) from raw mail
// bytes. It reads a copy of the message; callers that also need the raw
// bytes for storage or relay must keep their own reference -- this
// function never consumes the caller's copy of raw.
func parseMessageMetadata(raw []byte) (messageMetadata, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return messageMetadata{}, fmt.Errorf("failed to parse message: %w", err)
	}

	var meta messageMetadata
	if subject, err := mr.Header.Subject(); err == nil {
		meta.Subject = subject
	}

	index := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return meta, fmt.Errorf("failed to read message part: %w", err)
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			body, _ := io.ReadAll(p.Body)
			switch {
			case strings.HasPrefix(contentType, "text/plain"):
				meta.TextBody = string(body)
			case strings.HasPrefix(contentType, "text/html"):
				meta.HTMLBody = string(body)
			}
		case *mail.AttachmentHeader:
			contentType, _, _ := h.ContentType()
			filename, _ := h.Filename()
			body, _ := io.ReadAll(p.Body)
			meta.Attachments = append(meta.Attachments, attachmentMetadata{
				Index:       index,
				Filename:    filename,
				ContentType: contentType,
				SizeBytes:   len(body),
			})
			index++
		}
	}

	return meta, nil
}

// extractedPart holds one attachment's bytes for preview or download.
type extractedPart struct {
	Filename    string
	ContentType string
	Body        []byte
}

// extractPart re-parses raw and returns the bytes, declared content type,
// and filename of the attachment at the given 0-based index -- counting
// only attachment parts, in the same order parseMessageMetadata assigns
// them. Re-parsing on demand, rather than caching extracted bytes,
// matches research R11: the raw message is the only stored copy.
func extractPart(raw []byte, index int) (extractedPart, error) {
	if index < 0 {
		return extractedPart{}, fmt.Errorf("invalid attachment index %d", index)
	}

	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return extractedPart{}, fmt.Errorf("failed to parse message: %w", err)
	}

	current := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return extractedPart{}, fmt.Errorf("attachment index %d not found", index)
		}
		if err != nil {
			return extractedPart{}, fmt.Errorf("failed to read message part: %w", err)
		}

		h, ok := p.Header.(*mail.AttachmentHeader)
		if !ok {
			continue
		}

		if current == index {
			body, err := io.ReadAll(p.Body)
			if err != nil {
				return extractedPart{}, fmt.Errorf("failed to read attachment body: %w", err)
			}
			contentType, _, _ := h.ContentType()
			filename, _ := h.Filename()
			return extractedPart{Filename: filename, ContentType: contentType, Body: body}, nil
		}
		current++
	}
}

// messageHasAttachments reports whether raw contains at least one
// attachment part, without reading any part body -- cheap enough to call
// once per row when listing the queue, unlike parseMessageMetadata which
// reads every part fully to compute sizes.
func messageHasAttachments(raw []byte) bool {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			return false
		}
		if _, ok := p.Header.(*mail.AttachmentHeader); ok {
			return true
		}
	}
}

// previewableContentTypes are renderable inline in the portal without a
// download (FR-016a): images, PDF, and plain text. Everything else is
// download-only (FR-016b).
func isPreviewableContentType(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return true
	case contentType == "application/pdf":
		return true
	case strings.HasPrefix(contentType, "text/plain"):
		return true
	default:
		return false
	}
}

// normalizeAddress lower-cases and trims an address for comparison. Used
// wherever an envelope address is compared against one parsed from a
// header, so "Ops" <A@Example.COM> in a header matches envelope
// a@example.com.
func normalizeAddress(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// hiddenRecipients reports, for each envelope recipient, whether that
// address is absent from the message's visible To/Cc headers -- i.e.
// blind-carbon (FR-015a). An address that cannot be resolved because a
// header fails to parse is treated as hidden: this deliberately surfaces
// more addresses to the reviewer, never fewer (research R10). Hidden
// addresses must never be added to the message's visible headers when
// relayed (FR-015b) -- this function only classifies, it never mutates.
func hiddenRecipients(header *mail.Header, envelopeRecipients []string) map[string]bool {
	visible := make(map[string]struct{})
	for _, key := range []string{"To", "Cc"} {
		addrs, err := header.AddressList(key)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			visible[normalizeAddress(a.Address)] = struct{}{}
		}
	}

	hidden := make(map[string]bool, len(envelopeRecipients))
	for _, addr := range envelopeRecipients {
		_, isVisible := visible[normalizeAddress(addr)]
		hidden[addr] = !isVisible
	}
	return hidden
}

// rewriteHeaders mutates h in place for relaying: From is replaced with
// relayIdentity (FR-013), Reply-To is set to originalFrom only if h does
// not already carry a non-blank Reply-To of its own (FR-013a) -- a
// sender's explicit choice always wins -- and Sender is removed so it
// cannot contradict the rewritten From. Every other field is left alone.
// This function never touches the message body; see relayRewriteMessage
// for the full byte-level pipeline.
func rewriteHeaders(h *textproto.Header, relayIdentity, originalFrom string) {
	h.Set("From", relayIdentity)

	if strings.TrimSpace(h.Get("Reply-To")) == "" {
		h.Set("Reply-To", originalFrom)
	}

	h.Del("Sender")
}

// relayRewriteMessage applies rewriteHeaders to raw's header section and
// returns the result with the body copied through byte-for-byte (FR-008,
// SC-006): the only permitted differences from the original are the
// header fields rewriteHeaders changes.
func relayRewriteMessage(raw []byte, relayIdentity, originalFrom string) ([]byte, error) {
	br := bufio.NewReader(bytes.NewReader(raw))

	h, err := textproto.ReadHeader(br)
	if err != nil {
		return nil, fmt.Errorf("failed to read message header: %w", err)
	}

	rewriteHeaders(&h, relayIdentity, originalFrom)

	var buf bytes.Buffer
	if err := textproto.WriteHeader(&buf, h); err != nil {
		return nil, fmt.Errorf("failed to write rewritten header: %w", err)
	}
	if _, err := io.Copy(&buf, br); err != nil {
		return nil, fmt.Errorf("failed to copy message body: %w", err)
	}

	return buf.Bytes(), nil
}
