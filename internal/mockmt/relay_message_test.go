package mockmt

import (
	"bufio"
	"bytes"
	"io"
	"testing"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
)

type testAttachment struct {
	Filename    string
	ContentType string
	Body        []byte
}

type testMessageOptions struct {
	From     string
	To       []string
	Cc       []string
	ReplyTo  string
	Subject  string
	TextBody string
	HTMLBody string
	Attach   *testAttachment
}

// buildTestMessage constructs a well-formed raw MIME message using the
// same go-message library the production code parses with, so fixtures
// are realistic rather than hand-crafted strings that might not reflect
// real wire format.
func buildTestMessage(t *testing.T, opts testMessageOptions) []byte {
	t.Helper()

	var h mail.Header
	h.SetAddressList("From", []*mail.Address{{Address: opts.From}})
	if len(opts.To) > 0 {
		h.SetAddressList("To", addressList(opts.To))
	}
	if len(opts.Cc) > 0 {
		h.SetAddressList("Cc", addressList(opts.Cc))
	}
	if opts.ReplyTo != "" {
		h.SetAddressList("Reply-To", addressList([]string{opts.ReplyTo}))
	}
	h.SetSubject(opts.Subject)

	var buf bytes.Buffer
	mw, err := mail.CreateWriter(&buf, h)
	if err != nil {
		t.Fatalf("failed to create mail writer: %v", err)
	}

	switch {
	case opts.TextBody != "" && opts.HTMLBody != "":
		iw, err := mw.CreateInline()
		if err != nil {
			t.Fatalf("failed to create multipart/alternative writer: %v", err)
		}
		writeInlinePart(t, iw, "text/plain; charset=utf-8", opts.TextBody)
		writeInlinePart(t, iw, "text/html; charset=utf-8", opts.HTMLBody)
		if err := iw.Close(); err != nil {
			t.Fatalf("failed to close multipart/alternative writer: %v", err)
		}
	case opts.TextBody != "":
		writeSingleInlinePart(t, mw, "text/plain; charset=utf-8", opts.TextBody)
	case opts.HTMLBody != "":
		writeSingleInlinePart(t, mw, "text/html; charset=utf-8", opts.HTMLBody)
	}

	if opts.Attach != nil {
		var ah mail.AttachmentHeader
		ah.SetFilename(opts.Attach.Filename)
		ah.Set("Content-Type", opts.Attach.ContentType)
		w, err := mw.CreateAttachment(ah)
		if err != nil {
			t.Fatalf("failed to create attachment: %v", err)
		}
		if _, err := w.Write(opts.Attach.Body); err != nil {
			t.Fatalf("failed to write attachment body: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("failed to close attachment: %v", err)
		}
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("failed to close mail writer: %v", err)
	}

	return buf.Bytes()
}

func writeSingleInlinePart(t *testing.T, mw *mail.Writer, contentType, body string) {
	t.Helper()
	var ih mail.InlineHeader
	ih.Set("Content-Type", contentType)
	w, err := mw.CreateSingleInline(ih)
	if err != nil {
		t.Fatalf("failed to create inline part: %v", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatalf("failed to write inline body: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close inline part: %v", err)
	}
}

func writeInlinePart(t *testing.T, iw *mail.InlineWriter, contentType, body string) {
	t.Helper()
	var ih mail.InlineHeader
	ih.Set("Content-Type", contentType)
	w, err := iw.CreatePart(ih)
	if err != nil {
		t.Fatalf("failed to create inline part: %v", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatalf("failed to write inline body: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close inline part: %v", err)
	}
}

func addressList(addrs []string) []*mail.Address {
	list := make([]*mail.Address, len(addrs))
	for i, a := range addrs {
		list[i] = &mail.Address{Address: a}
	}
	return list
}

// --- T027: rewriteHeaders Reply-To precedence ---

func readHeaderOnly(t *testing.T, raw []byte) textproto.Header {
	t.Helper()
	h, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("failed to read header: %v", err)
	}
	return h
}

func TestRewriteHeadersReplyToPrecedence(t *testing.T) {
	const relayIdentity = "relay@example.com"
	const originalFrom = "agent@myapp.local"

	tests := []struct {
		name          string
		existingReply string // "" means no Reply-To header at all
		wantReplyTo   string
	}{
		{name: "absent", existingReply: "", wantReplyTo: originalFrom},
		{name: "present and non-blank", existingReply: "sender-chosen@example.com", wantReplyTo: "sender-chosen@example.com"},
		{name: "present but empty", existingReply: "", wantReplyTo: originalFrom},
		{name: "present but whitespace-only", existingReply: "   ", wantReplyTo: originalFrom},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var h textproto.Header
			h.Set("From", originalFrom)
			h.Set("Subject", "test")
			if tc.name != "absent" {
				// Force the header to exist even for the blank/whitespace
				// cases, which Set alone would also do, but this makes
				// the "no header at all" vs "header present but blank"
				// distinction explicit for the reader.
				h.Set("Reply-To", tc.existingReply)
			}

			rewriteHeaders(&h, relayIdentity, originalFrom)

			if got := h.Get("From"); got != relayIdentity {
				t.Errorf("From = %q, want %q", got, relayIdentity)
			}
			if got := h.Get("Reply-To"); got != tc.wantReplyTo {
				t.Errorf("Reply-To = %q, want %q", got, tc.wantReplyTo)
			}
		})
	}
}

func TestRewriteHeadersPreservesDuplicateReplyToUntouched(t *testing.T) {
	var h textproto.Header
	h.Set("From", "agent@myapp.local")
	h.Add("Reply-To", "first@example.com")
	h.Add("Reply-To", "second@example.com")

	rewriteHeaders(&h, "relay@example.com", "agent@myapp.local")

	// A duplicated, non-blank Reply-To is still the sender's explicit
	// choice: Header.Get sees a non-blank value, so rewriteHeaders' Reply-To
	// branch must not fire at all, leaving both original lines exactly as
	// they were (FR-013a). (Header.Add/Values treat the most recently
	// added field as first on retrieval -- this asserts non-mutation, not
	// a specific ordering.)
	got := h.Values("Reply-To")
	want := []string{"second@example.com", "first@example.com"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Reply-To values = %v, want %v unchanged", got, want)
	}
}

func TestRewriteHeadersDeletesSender(t *testing.T) {
	var h textproto.Header
	h.Set("From", "agent@myapp.local")
	h.Set("Sender", "someone-else@myapp.local")

	rewriteHeaders(&h, "relay@example.com", "agent@myapp.local")

	if h.Has("Sender") {
		t.Error("expected Sender to be removed so it cannot contradict the rewritten From")
	}
}

// --- T028: golden byte-identical body test ---

func TestRelayRewriteMessagePreservesBodyByteForByte(t *testing.T) {
	raw := buildTestMessage(t, testMessageOptions{
		From:     "agent@myapp.local",
		To:       []string{"customer@example.com"},
		Subject:  "Your quote",
		TextBody: "Hello, here is your quote.",
		Attach:   &testAttachment{Filename: "quote.pdf", ContentType: "application/pdf", Body: []byte("%PDF-1.4 fake pdf bytes")},
	})

	rewritten, err := relayRewriteMessage(raw, "relay@example.com", "agent@myapp.local")
	if err != nil {
		t.Fatalf("relayRewriteMessage failed: %v", err)
	}

	originalBody := bodyAfterHeader(t, raw)
	rewrittenBody := bodyAfterHeader(t, rewritten)

	if !bytes.Equal(originalBody, rewrittenBody) {
		t.Fatalf("body bytes changed after header rewrite:\noriginal:  %q\nrewritten: %q", originalBody, rewrittenBody)
	}

	h := readHeaderOnly(t, rewritten)
	if got := h.Get("From"); got != "relay@example.com" {
		t.Errorf("From = %q, want relay@example.com", got)
	}
	if got := h.Get("Reply-To"); got != "agent@myapp.local" {
		t.Errorf("Reply-To = %q, want agent@myapp.local", got)
	}

	// The rewritten message must still parse and expose the same
	// attachment content -- rewriting headers must not corrupt MIME
	// structure.
	meta, err := parseMessageMetadata(rewritten)
	if err != nil {
		t.Fatalf("failed to parse rewritten message: %v", err)
	}
	if len(meta.Attachments) != 1 || meta.Attachments[0].Filename != "quote.pdf" {
		t.Fatalf("rewritten message lost its attachment: %+v", meta.Attachments)
	}
}

func bodyAfterHeader(t *testing.T, raw []byte) []byte {
	t.Helper()
	br := bufio.NewReader(bytes.NewReader(raw))
	if _, err := textproto.ReadHeader(br); err != nil {
		t.Fatalf("failed to read header: %v", err)
	}
	body, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return body
}

// --- T029: envelope vs header recipients ---

func TestHiddenRecipientsMarksBccButNotToOrCc(t *testing.T) {
	raw := buildTestMessage(t, testMessageOptions{
		From:     "agent@myapp.local",
		To:       []string{"customer@example.com"},
		Cc:       []string{"manager@example.com"},
		Subject:  "test",
		TextBody: "body",
	})

	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	envelope := []string{"customer@example.com", "manager@example.com", "audit@example.com"}
	hidden := hiddenRecipients(&mr.Header, envelope)

	if hidden["customer@example.com"] {
		t.Error("To recipient must not be marked hidden")
	}
	if hidden["manager@example.com"] {
		t.Error("Cc recipient must not be marked hidden")
	}
	if !hidden["audit@example.com"] {
		t.Error("envelope-only (Bcc) recipient must be marked hidden")
	}
}

func TestHiddenRecipientsMatchesCaseInsensitivelyAndIgnoresDisplayNames(t *testing.T) {
	var h mail.Header
	h.SetAddressList("To", []*mail.Address{{Name: "Ops", Address: "Ops@Example.COM"}})

	hidden := hiddenRecipients(&h, []string{"ops@example.com"})

	if hidden["ops@example.com"] {
		t.Error("expected case-insensitive, display-name-agnostic match to mark the recipient visible")
	}
}

func TestHiddenRecipientsTreatsUnparseableHeaderAsHidden(t *testing.T) {
	var h mail.Header
	// An unterminated quoted string is not a valid address-list value.
	h.Set("To", `"unterminated quote <customer@example.com>`)

	hidden := hiddenRecipients(&h, []string{"customer@example.com"})

	if !hidden["customer@example.com"] {
		t.Error("an unparseable To header must resolve to hidden, not visible (research R10: surface more, never less)")
	}
}
