package mockmt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-smtp"
	"github.com/gin-gonic/gin"
)

// setRelayConfigForTest installs cfg as the active relay configuration for
// the duration of the test and restores nil afterwards, since relayCfg is
// a package-level var shared across the whole test binary run.
func setRelayConfigForTest(t *testing.T, cfg *RelayConfig) {
	t.Helper()
	InitRelay(cfg)
	t.Cleanup(func() {
		relayCfg = nil
		relayIOSem = nil
	})
}

func newTestJWTSecret(t *testing.T) {
	t.Helper()
	jwtSecret = []byte("test-secret-key")
}

func authTokenFor(t *testing.T, email string) string {
	t.Helper()
	token, err := generateJWT(email, 1)
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}
	return token
}

// newTestRelayRouter mirrors the relay-relevant subset of StartWebServer's
// route wiring in web.go, without needing OAuth env vars or a bound port.
func newTestRelayRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	api := r.Group("/api")
	api.Use(authMiddleware())
	{
		api.GET("/relay/status", handleRelayStatus)

		relay := api.Group("/relay")
		relay.Use(relayModeGate(), requireReviewer())
		{
			relay.GET("/queue", handleRelayQueue)
			relay.GET("/messages/:id", handleRelayMessageDetail)
			relay.GET("/messages/:id/attachments/:index", handleRelayAttachment)
			relay.POST("/messages/:id/send", handleRelaySend)
			relay.POST("/messages/:id/reject", handleRelayReject)
			relay.GET("/messages/:id/audit", handleRelayAudit)
		}
	}

	return r
}

func doRequest(r *gin.Engine, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRelayAPIReturns404WhenDisabled(t *testing.T) {
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{Enabled: false})

	r := newTestRelayRouter()
	token := authTokenFor(t, "anyone@example.com")

	w := doRequest(r, http.MethodGet, "/api/relay/queue", token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /api/relay/queue with relay disabled = %d, want 404", w.Code)
	}
}

func TestRelayStatusRespondsEvenWhenDisabled(t *testing.T) {
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{Enabled: false})

	r := newTestRelayRouter()
	token := authTokenFor(t, "anyone@example.com")

	w := doRequest(r, http.MethodGet, "/api/relay/status", token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/relay/status with relay disabled = %d, want 200", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["relay_enabled"] != false {
		t.Errorf("relay_enabled = %v, want false", body["relay_enabled"])
	}
}

func TestRequireReviewerRejectsNonReviewer(t *testing.T) {
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	r := newTestRelayRouter()
	token := authTokenFor(t, "bob@example.com")

	// FR-018: a non-reviewer must be refused, even for a path that carries
	// no message-specific identifier -- there is no "but it's addressed to
	// me" exception anywhere in the relay API.
	w := doRequest(r, http.MethodGet, "/api/relay/queue", token)
	if w.Code != http.StatusForbidden {
		t.Fatalf("GET /api/relay/queue as a non-reviewer = %d, want 403", w.Code)
	}
}

func TestRequireReviewerAllowsReviewer(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")

	w := doRequest(r, http.MethodGet, "/api/relay/queue", token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/relay/queue as a reviewer = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

func TestRelayStatusReportsReviewerStatus(t *testing.T) {
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		Identity:  "relay@example.com",
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	r := newTestRelayRouter()

	for _, tc := range []struct {
		email    string
		reviewer bool
	}{
		{"alice@example.com", true},
		{"bob@example.com", false},
	} {
		w := doRequest(r, http.MethodGet, "/api/relay/status", authTokenFor(t, tc.email))
		if w.Code != http.StatusOK {
			t.Fatalf("status for %s = %d, want 200", tc.email, w.Code)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if body["is_reviewer"] != tc.reviewer {
			t.Errorf("is_reviewer for %s = %v, want %v", tc.email, body["is_reviewer"], tc.reviewer)
		}
	}
}

// T033: queue listing, message detail, and a successful send, exercised
// end to end against the real handlers and a real (fake) upstream.
func TestRelayAPIQueueDetailAndSend(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)

	up := startFakeUpstream(t, fakeUpstreamOptions{})
	cfg := relayConfigForUpstream(t, up)
	cfg.Enabled = true
	cfg.reviewers = map[string]struct{}{"alice@example.com": {}}
	setRelayConfigForTest(t, cfg)

	raw := buildTestMessage(t, testMessageOptions{
		From:     "agent@myapp.local",
		To:       []string{"customer@example.com"},
		Subject:  "Your quote",
		TextBody: "Hello, here is your quote.",
	})
	id, err := insertQueuedMessage("agent@myapp.local", "agent@myapp.local", "Your quote", raw, false, []queuedRecipientInput{
		{Address: "customer@example.com", Hidden: false},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")

	// Queue listing.
	w := doRequest(r, http.MethodGet, "/api/relay/queue?state=pending_review", token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/relay/queue = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var queueBody map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &queueBody); err != nil {
		t.Fatalf("failed to decode queue response: %v", err)
	}
	if queueBody["total"].(float64) != 1 {
		t.Errorf("queue total = %v, want 1", queueBody["total"])
	}

	// Message detail.
	detailPath := fmt.Sprintf("/api/relay/messages/%d", id)
	w = doRequest(r, http.MethodGet, detailPath, token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200, body=%s", detailPath, w.Code, w.Body.String())
	}
	var detail map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("failed to decode detail response: %v", err)
	}
	if detail["subject"] != "Your quote" {
		t.Errorf("subject = %v, want %q", detail["subject"], "Your quote")
	}
	if detail["text_body"] != "Hello, here is your quote." {
		t.Errorf("text_body = %v", detail["text_body"])
	}

	// Send.
	sendPath := fmt.Sprintf("/api/relay/messages/%d/send", id)
	w = doRequest(r, http.MethodPost, sendPath, token)
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200, body=%s", sendPath, w.Code, w.Body.String())
	}
	var sendResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &sendResp); err != nil {
		t.Fatalf("failed to decode send response: %v", err)
	}
	if sendResp["state"] != "sent" {
		t.Fatalf("state = %v, want sent, body=%s", sendResp["state"], w.Body.String())
	}
	if sendResp["decided_by"] != "alice@example.com" {
		t.Errorf("decided_by = %v, want alice@example.com", sendResp["decided_by"])
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "sent" {
		t.Errorf("final state = %q, want sent", msg.State)
	}
}

// T034, FR-018: a non-reviewer must be refused by every relay endpoint,
// including a message addressed to their own recipient address -- there
// is no "but it's mine" exception anywhere in the relay API, because
// queued messages have no owning user at all.
func TestNonReviewerForbiddenFromEveryEndpointIncludingOwnRecipientAddress(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"bob@example.com"}, Subject: "s", TextBody: "b"})
	id, err := insertQueuedMessage("agent@myapp.local", "agent@myapp.local", "s", raw, false, []queuedRecipientInput{
		{Address: "bob@example.com", Hidden: false},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	r := newTestRelayRouter()
	// bob@example.com is the message's own recipient, but is not a
	// reviewer.
	token := authTokenFor(t, "bob@example.com")

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/relay/queue"},
		{http.MethodGet, fmt.Sprintf("/api/relay/messages/%d", id)},
		{http.MethodGet, fmt.Sprintf("/api/relay/messages/%d/attachments/0", id)},
		{http.MethodPost, fmt.Sprintf("/api/relay/messages/%d/send", id)},
		{http.MethodPost, fmt.Sprintf("/api/relay/messages/%d/reject", id)},
		{http.MethodGet, fmt.Sprintf("/api/relay/messages/%d/audit", id)},
	}
	for _, tc := range cases {
		w := doRequest(r, tc.method, tc.path, token)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-reviewer bob (message's own recipient) = %d, want 403", tc.method, tc.path, w.Code)
		}
	}
}

// T054, US3: rejecting a pending message moves it to rejected, attempts
// no delivery, and records the decider; a second reject or a send
// afterwards must both be refused as a state conflict.
func TestRejectMovesToRejectedAndBlocksFurtherAction(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s", TextBody: "b"})
	id, err := insertQueuedMessage("agent@myapp.local", "", "s", raw, false, []queuedRecipientInput{{Address: "customer@example.com"}})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")
	rejectPath := fmt.Sprintf("/api/relay/messages/%d/reject", id)

	w := doRequest(r, http.MethodPost, rejectPath, token)
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200, body=%s", rejectPath, w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["state"] != "rejected" {
		t.Errorf("state = %v, want rejected", resp["state"])
	}
	if resp["decided_by"] != "alice@example.com" {
		t.Errorf("decided_by = %v, want alice@example.com", resp["decided_by"])
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "rejected" {
		t.Fatalf("final state = %q, want rejected", msg.State)
	}
	if msg.DecidedBy != "alice@example.com" {
		t.Errorf("DecidedBy = %q, want alice@example.com", msg.DecidedBy)
	}

	// A second reject must be a state conflict, not a silent success.
	w = doRequest(r, http.MethodPost, rejectPath, token)
	if w.Code != http.StatusConflict {
		t.Errorf("second reject = %d, want 409", w.Code)
	}

	// A send afterwards must also be a state conflict -- rejection is terminal.
	sendPath := fmt.Sprintf("/api/relay/messages/%d/send", id)
	w = doRequest(r, http.MethodPost, sendPath, token)
	if w.Code != http.StatusConflict {
		t.Errorf("send after reject = %d, want 409", w.Code)
	}
}

// T063, FR-025a: retrying a failed-indeterminate message without
// confirm_duplicate_risk must be refused (422), and must succeed once the
// reviewer explicitly accepts the duplicate risk.
func TestRetryIndeterminateRequiresConfirmation(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)

	up := startFakeUpstream(t, fakeUpstreamOptions{})
	cfg := relayConfigForUpstream(t, up)
	cfg.Enabled = true
	cfg.reviewers = map[string]struct{}{"alice@example.com": {}}
	setRelayConfigForTest(t, cfg)

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s", TextBody: "b"})
	id, err := insertQueuedMessage("agent@myapp.local", "", "s", raw, false, []queuedRecipientInput{{Address: "customer@example.com"}})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	// Drive the message into failed/indeterminate directly, simulating a
	// prior attempt whose final-dot acknowledgement never arrived.
	claimed, _, _, err := tryClaimMessageForSend(id, "alice@example.com", false)
	if err != nil || !claimed {
		t.Fatalf("failed to claim message for setup: claimed=%v err=%v", claimed, err)
	}
	if err := markFailed(id, "indeterminate", "no acknowledgement received", "alice@example.com", nil); err != nil {
		t.Fatalf("markFailed failed: %v", err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")
	sendPath := fmt.Sprintf("/api/relay/messages/%d/send", id)

	// Without confirmation: refused.
	w := doRequest(r, http.MethodPost, sendPath, token)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("retry without confirmation = %d, want 422, body=%s", w.Code, w.Body.String())
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "failed" {
		t.Fatalf("state after refused retry = %q, want failed (must not have been claimed)", msg.State)
	}

	// With confirmation: succeeds.
	req := httptest.NewRequest(http.MethodPost, sendPath, strings.NewReader(`{"confirm_duplicate_risk": true}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("retry with confirmation = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["state"] != "sent" {
		t.Fatalf("state = %v, want sent, body=%s", resp["state"], w.Body.String())
	}
}

// T077, FR-036: a purged message reports purged: true with content
// omitted from detail, and attachment fetches return 410 rather than
// pretending it is retriable or showing a blank message.
func TestPurgedMessageDetailAndAttachmentHandling(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	raw := buildTestMessage(t, testMessageOptions{
		From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s", TextBody: "b",
		Attach: &testAttachment{Filename: "f.txt", ContentType: "text/plain", Body: []byte("data")},
	})
	id := insertTerminalMessageAt(t, "sent", 100)
	if _, err := db.Exec(`UPDATE queued_messages SET raw_message = ? WHERE id = ?`, raw, id); err != nil {
		t.Fatalf("failed to set up message: %v", err)
	}
	if n, err := PurgeExpiredMessages(90); err != nil || n != 1 {
		t.Fatalf("PurgeExpiredMessages: n=%d err=%v", n, err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")

	w := doRequest(r, http.MethodGet, fmt.Sprintf("/api/relay/messages/%d", id), token)
	if w.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var detail map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if detail["purged"] != true {
		t.Errorf("purged = %v, want true", detail["purged"])
	}
	if _, hasBody := detail["text_body"]; hasBody {
		t.Error("expected text_body to be omitted for a purged message")
	}

	w = doRequest(r, http.MethodGet, fmt.Sprintf("/api/relay/messages/%d/attachments/0", id), token)
	if w.Code != http.StatusGone {
		t.Errorf("attachment fetch on purged message = %d, want 410", w.Code)
	}
}

// T078, SC-008b: response headers on the detail and attachment endpoints
// must prevent active content from executing in, or reading from, the
// portal's security context. This asserts the headers a browser needs to
// enforce that -- the actual sandboxing (no allow-scripts, no
// allow-same-origin) additionally happens client-side in the iframe that
// renders html_body, which a Go test cannot exercise directly.
func TestDetailAndAttachmentResponsesCarryIsolationHeaders(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	raw := buildTestMessage(t, testMessageOptions{
		From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s",
		HTMLBody: `<html><body><script>window.parent.postMessage(document.cookie, "*")</script><h1>hostile</h1></body></html>`,
		Attach:   &testAttachment{Filename: "evil.html", ContentType: "text/html", Body: []byte("<script>alert(1)</script>")},
	})
	id, err := insertQueuedMessage("agent@myapp.local", "", "s", raw, true, []queuedRecipientInput{{Address: "customer@example.com"}})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")

	w := doRequest(r, http.MethodGet, fmt.Sprintf("/api/relay/messages/%d", id), token)
	if w.Code != http.StatusOK {
		t.Fatalf("detail = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("detail X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("detail response missing Content-Security-Policy")
	}

	w = doRequest(r, http.MethodGet, fmt.Sprintf("/api/relay/messages/%d/attachments/0", id), token)
	if w.Code != http.StatusOK {
		t.Fatalf("attachment fetch = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("attachment X-Content-Type-Options = %q, want nosniff", got)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") {
		t.Errorf("attachment Content-Security-Policy = %q, want it to include sandbox", csp)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("attachment Content-Disposition = %q, want attachment (not inline) by default", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("attachment Cache-Control = %q, want no-store", got)
	}
}

// T079, FR-016d, SC-008c: opening a message for review must not itself
// cause the server to fetch anything from a sender-controlled or
// third-party host. This is inherent in the design (the backend never
// makes outbound HTTP requests while parsing or serving a message; the
// client-side isolation that blocks the browser from fetching remote
// content live in the iframe's in-document CSP, verified by inspection of
// ReviewMessage.vue's sandboxedHtmlDoc), but is asserted here at the
// level a Go test can reach: parsing a message with remote references
// does not, by itself, trigger any network activity or leak into the
// parsed output as a fetchable reference the backend resolves.
func TestReviewingMessageTriggersNoRemoteFetch(t *testing.T) {
	raw := buildTestMessage(t, testMessageOptions{
		From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s",
		HTMLBody: `<html><body><img src="http://example-tracker.invalid/pixel.gif"></body></html>`,
	})

	meta, err := parseMessageMetadata(raw)
	if err != nil {
		t.Fatalf("parseMessageMetadata failed: %v", err)
	}

	// The raw HTML (including the remote reference) is returned verbatim
	// for the client to render inside its sandboxed, CSP-restricted
	// iframe -- parsing it here must not have caused any network access
	// itself. There is no HTTP client anywhere in parseMessageMetadata or
	// extractPart, so this is structurally guaranteed; the assertion
	// below confirms the content reaching the client is unmodified rather
	// than, say, having been pre-fetched and inlined.
	if !strings.Contains(meta.HTMLBody, "example-tracker.invalid") {
		t.Fatal("expected the remote reference to pass through unresolved for client-side sandboxing")
	}
}

// Regression test: partial recipient failure must not be recorded as a
// full success. If ANY recipient is rejected, the message must land in
// "failed" (not "sent", which is terminal and would make the rejected
// recipient permanently unretriable), the delivered recipient's own
// upstream response must be persisted, the rejected recipient's
// rejection reason must be persisted too (not just returned once in the
// HTTP body and dropped), and a subsequent retry must succeed while
// re-attempting only the recipient that was not yet served.
func TestPartialSendFailureStaysRetriableAndRetrySucceeds(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)

	rejectingUpstream := startFakeUpstream(t, fakeUpstreamOptions{RejectRecipient: "bad@example.com"})
	cfg := relayConfigForUpstream(t, rejectingUpstream)
	cfg.Enabled = true
	cfg.reviewers = map[string]struct{}{"alice@example.com": {}}
	setRelayConfigForTest(t, cfg)

	raw := buildTestMessage(t, testMessageOptions{
		From: "agent@myapp.local", To: []string{"good@example.com", "bad@example.com"}, Subject: "s", TextBody: "b",
	})
	id, err := insertQueuedMessage("agent@myapp.local", "", "s", raw, false, []queuedRecipientInput{
		{Address: "good@example.com"},
		{Address: "bad@example.com"},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")
	sendPath := fmt.Sprintf("/api/relay/messages/%d/send", id)

	w := doRequest(r, http.MethodPost, sendPath, token)
	if w.Code != http.StatusOK {
		t.Fatalf("first send = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var firstResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if firstResp["state"] != "failed" {
		t.Fatalf("state after partial failure = %v, want failed (must stay retriable)", firstResp["state"])
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "failed" {
		t.Fatalf("persisted state = %q, want failed", msg.State)
	}
	byAddr := map[string]QueuedRecipient{}
	for _, rec := range msg.Recipients {
		byAddr[rec.Address] = rec
	}
	if !byAddr["good@example.com"].Delivered {
		t.Error("good@example.com must be marked delivered")
	}
	if byAddr["good@example.com"].UpstreamResponse == "" {
		t.Error("good@example.com must have its own upstream_response persisted")
	}
	if byAddr["bad@example.com"].Delivered {
		t.Error("bad@example.com must not be marked delivered")
	}
	if byAddr["bad@example.com"].UpstreamResponse == "" {
		t.Error("bad@example.com's rejection reason must be persisted, not just returned once in the HTTP response")
	}

	// Point relay at an upstream that accepts everyone, then retry. Only
	// the still-unserved recipient should be attempted.
	healthyUpstream := startFakeUpstream(t, fakeUpstreamOptions{})
	cfg2 := relayConfigForUpstream(t, healthyUpstream)
	cfg2.Enabled = true
	cfg2.reviewers = map[string]struct{}{"alice@example.com": {}}
	setRelayConfigForTest(t, cfg2)

	w = doRequest(r, http.MethodPost, sendPath, token)
	if w.Code != http.StatusOK {
		t.Fatalf("retry = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var retryResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &retryResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if retryResp["state"] != "sent" {
		t.Fatalf("state after retry = %v, want sent, body=%s", retryResp["state"], w.Body.String())
	}
	retryRecipients, ok := retryResp["recipients"].([]interface{})
	if !ok || len(retryRecipients) != 1 {
		t.Fatalf("retry attempted %d recipients, want exactly 1 (only the previously-unserved one)", len(retryRecipients))
	}

	finalMsg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if finalMsg.State != "sent" {
		t.Fatalf("final state = %q, want sent", finalMsg.State)
	}
	for _, rec := range finalMsg.Recipients {
		if !rec.Delivered {
			t.Errorf("recipient %s not delivered after successful retry", rec.Address)
		}
	}
}

// Regression test: has_attachments in the queue listing must reflect
// reality, computed once at ingest and persisted -- not derived from
// RawMessage, which listQueue never selects (and is nil for every row as
// a result).
func TestQueueListingReportsHasAttachmentsAccurately(t *testing.T) {
	setupTestDB(t)
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	s := &Session{backend: &Backend{Username: "user", Password: "pass"}}
	authenticate(t, s, "user", "pass")

	if err := s.Mail("agent@myapp.local", &smtp.MailOptions{}); err != nil {
		t.Fatalf("Mail failed: %v", err)
	}
	if err := s.Rcpt("plain@example.com", &smtp.RcptOptions{}); err != nil {
		t.Fatalf("Rcpt failed: %v", err)
	}
	if err := s.Data(strings.NewReader("Subject: no attachment\r\nFrom: agent@myapp.local\r\nTo: plain@example.com\r\n\r\nHello\r\n")); err != nil {
		t.Fatalf("Data failed: %v", err)
	}
	s.Reset()

	if err := s.Mail("agent@myapp.local", &smtp.MailOptions{}); err != nil {
		t.Fatalf("Mail failed: %v", err)
	}
	if err := s.Rcpt("withattach@example.com", &smtp.RcptOptions{}); err != nil {
		t.Fatalf("Rcpt failed: %v", err)
	}
	withAttachment := buildTestMessage(t, testMessageOptions{
		From: "agent@myapp.local", To: []string{"withattach@example.com"}, Subject: "has attachment", TextBody: "b",
		Attach: &testAttachment{Filename: "f.txt", ContentType: "text/plain", Body: []byte("data")},
	})
	if err := s.Data(strings.NewReader(string(withAttachment))); err != nil {
		t.Fatalf("Data failed: %v", err)
	}

	r := newTestRelayRouter()
	token := authTokenFor(t, "alice@example.com")
	w := doRequest(r, http.MethodGet, "/api/relay/queue?state=all&limit=10", token)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/relay/queue = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Messages []struct {
			Subject        string `json:"subject"`
			HasAttachments bool   `json:"has_attachments"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	bySubject := map[string]bool{}
	for _, m := range body.Messages {
		bySubject[m.Subject] = m.HasAttachments
	}
	if bySubject["no attachment"] {
		t.Error(`"no attachment" message reported has_attachments = true, want false`)
	}
	if !bySubject["has attachment"] {
		t.Error(`"has attachment" message reported has_attachments = false, want true`)
	}
}

// Regression test for the rescue mechanism a stranded-claim fix depends
// on: if a downstream error occurs after a message has been claimed
// (moved to "sending") but before any network attempt is made, the
// handler settles it back via markFailed rather than leaving it stuck.
// Reproducing the exact getQueuedMessage/startDeliveryAttempt failure
// through the live HTTP handler would require a fault-injection seam
// this codebase doesn't have; this test instead verifies the rescue's
// consequence directly -- that a message settled this way is genuinely
// recoverable, not just nominally in a different state.
func TestClaimedMessageSettledByPreflightErrorRemainsRecoverable(t *testing.T) {
	setupTestDB(t)

	id, err := insertQueuedMessage("agent@myapp.local", "", "s", []byte("raw"), false, []queuedRecipientInput{
		{Address: "customer@example.com"},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	claimed, _, _, err := tryClaimMessageForSend(id, "alice@example.com", false)
	if err != nil || !claimed {
		t.Fatalf("failed to claim: claimed=%v err=%v", claimed, err)
	}

	// Simulate the handler's rescue path for a pre-send DB error.
	if err := markFailed(id, "confirmed", "failed to load message after claiming it for send", "alice@example.com", nil); err != nil {
		t.Fatalf("markFailed failed: %v", err)
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "failed" {
		t.Fatalf("state = %q, want failed (must not be stuck in sending)", msg.State)
	}

	// Recoverable: claimable again (retry), and abandonable (reject).
	claimed, _, _, err = tryClaimMessageForSend(id, "alice@example.com", false)
	if err != nil || !claimed {
		t.Fatalf("message is not retriable after the rescue: claimed=%v err=%v", claimed, err)
	}
}

// FR-006, SC-009: the status endpoint must never leak upstream
// credentials, host, or port -- only mode, reviewer status, and the
// display identity.
func TestRelayStatusNeverExposesCredentialsOrConnectionDetails(t *testing.T) {
	newTestJWTSecret(t)
	setRelayConfigForTest(t, &RelayConfig{
		Enabled:   true,
		Host:      "smtp.example.com",
		Port:      "587",
		Username:  "relay-user",
		Password:  "super-secret-password",
		Identity:  "relay@example.com",
		reviewers: map[string]struct{}{"alice@example.com": {}},
	})

	r := newTestRelayRouter()
	w := doRequest(r, http.MethodGet, "/api/relay/status", authTokenFor(t, "alice@example.com"))

	body := w.Body.String()
	for _, secret := range []string{"super-secret-password", "relay-user", "smtp.example.com", "587"} {
		if strings.Contains(body, secret) {
			t.Fatalf("status response leaked %q: %s", secret, body)
		}
	}
}
