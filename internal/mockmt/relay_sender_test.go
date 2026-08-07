package mockmt

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// TestTLSClientConfigForSetsServerNameWhenAbsent guards against a real
// bug found while building the e2e relay test rig: without ServerName
// set, x509.Certificate.Verify skips VerifyHostname entirely (see
// GOROOT crypto/x509/verify.go), so hostname verification was silently
// never happening even though chain-of-trust was.
func TestTLSClientConfigForSetsServerNameWhenAbsent(t *testing.T) {
	cfg := &RelayConfig{Host: "fake-upstream", TLSConfig: &tls.Config{}}

	got := tlsClientConfigFor(cfg)

	if got.ServerName != "fake-upstream" {
		t.Fatalf("expected ServerName to be filled in from cfg.Host, got %q", got.ServerName)
	}
	if cfg.TLSConfig.ServerName != "" {
		t.Fatalf("tlsClientConfigFor must not mutate the shared RelayConfig.TLSConfig in place")
	}
}

func TestTLSClientConfigForLeavesExplicitServerNameUntouched(t *testing.T) {
	cfg := &RelayConfig{Host: "fake-upstream", TLSConfig: &tls.Config{ServerName: "127.0.0.1"}}

	got := tlsClientConfigFor(cfg)

	if got.ServerName != "127.0.0.1" {
		t.Fatalf("expected explicit ServerName to be preserved, got %q", got.ServerName)
	}
}

func relayConfigForUpstream(t *testing.T, up fakeUpstream) *RelayConfig {
	t.Helper()

	host, port, err := net.SplitHostPort(up.Addr)
	if err != nil {
		t.Fatalf("failed to split fake upstream address: %v", err)
	}

	return &RelayConfig{
		Host:           host,
		Port:           port,
		Username:       "relay-user",
		Password:       "relay-pass",
		TLSMode:        "starttls",
		Identity:       "relay@example.com",
		TimeoutSeconds: 5,
		TLSConfig:      up.ClientTLS,
	}
}

// T032: happy path against the fake upstream -- From rewritten to the
// relay identity, Reply-To set to the original sender, body unchanged,
// and the recipient marked delivered.
func TestRelaySendHappyPath(t *testing.T) {
	up := startFakeUpstream(t, fakeUpstreamOptions{})
	cfg := relayConfigForUpstream(t, up)

	raw := buildTestMessage(t, testMessageOptions{
		From:     "agent@myapp.local",
		To:       []string{"customer@example.com"},
		Subject:  "Your quote",
		TextBody: "Hello, here is your quote.",
	})

	rewritten, err := relayRewriteMessage(raw, cfg.Identity, "agent@myapp.local")
	if err != nil {
		t.Fatalf("relayRewriteMessage failed: %v", err)
	}

	recipients := []QueuedRecipient{{Address: "customer@example.com"}}
	result := relaySend(cfg, cfg.Identity, recipients, rewritten)

	if result.Outcome != outcomeSent {
		t.Fatalf("Outcome = %q, want sent (reason: %s)", result.Outcome, result.FailureReason)
	}
	if result.UpstreamResponse == "" {
		t.Error("expected a non-empty upstream response on success")
	}
	if len(result.Recipients) != 1 || !result.Recipients[0].Delivered {
		t.Fatalf("Recipients = %+v, want exactly one delivered recipient", result.Recipients)
	}

	originalBody := bodyAfterHeader(t, raw)
	rewrittenBody := bodyAfterHeader(t, rewritten)
	if string(originalBody) != string(rewrittenBody) {
		t.Error("body must be unchanged by relaying, only headers may differ")
	}

	h := readHeaderOnly(t, rewritten)
	if got := h.Get("From"); got != cfg.Identity {
		t.Errorf("From = %q, want %q", got, cfg.Identity)
	}
	if got := h.Get("Reply-To"); got != "agent@myapp.local" {
		t.Errorf("Reply-To = %q, want agent@myapp.local", got)
	}
}

// T058: the fake upstream rejects one recipient and accepts another; the
// accepted one is marked delivered and the rejected one is not, and the
// overall outcome is still "sent" since at least one recipient succeeded.
func TestRelaySendPartialRecipientFailure(t *testing.T) {
	up := startFakeUpstream(t, fakeUpstreamOptions{RejectRecipient: "bad@example.com"})
	cfg := relayConfigForUpstream(t, up)

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"good@example.com", "bad@example.com"}, Subject: "s", TextBody: "b"})
	rewritten, err := relayRewriteMessage(raw, cfg.Identity, "agent@myapp.local")
	if err != nil {
		t.Fatalf("relayRewriteMessage failed: %v", err)
	}

	recipients := []QueuedRecipient{{Address: "good@example.com"}, {Address: "bad@example.com"}}
	result := relaySend(cfg, cfg.Identity, recipients, rewritten)

	// Not everyone got the message, so the message-level outcome must not
	// be "sent" -- that would make it terminal and permanently
	// unretriable for bad@example.com. It is also not indeterminate:
	// exactly what happened to each recipient is known, with no
	// ambiguity. Confirmed-failed is what keeps the message retriable
	// (FR-025) while still recording good@example.com as served.
	if result.Outcome != outcomeConfirmed {
		t.Fatalf("Outcome = %q, want confirmed_failed (a partial failure must not be reported as sent, or the unserved recipient could never be retried)", result.Outcome)
	}

	byAddr := map[string]recipientOutcome{}
	for _, r := range result.Recipients {
		byAddr[r.Address] = r
	}
	if !byAddr["good@example.com"].Delivered {
		t.Error("good@example.com should be delivered")
	}
	if byAddr["bad@example.com"].Delivered {
		t.Error("bad@example.com should not be delivered")
	}
	if byAddr["bad@example.com"].UpstreamResponse == "" {
		t.Error("expected a recorded upstream response explaining the rejection")
	}
}

func TestRelaySendConfirmedFailureWhenEveryRecipientRejected(t *testing.T) {
	up := startFakeUpstream(t, fakeUpstreamOptions{RejectRecipient: "only@example.com"})
	cfg := relayConfigForUpstream(t, up)

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"only@example.com"}, Subject: "s", TextBody: "b"})
	rewritten, err := relayRewriteMessage(raw, cfg.Identity, "agent@myapp.local")
	if err != nil {
		t.Fatalf("relayRewriteMessage failed: %v", err)
	}

	result := relaySend(cfg, cfg.Identity, []QueuedRecipient{{Address: "only@example.com"}}, rewritten)

	if result.Outcome != outcomeConfirmed {
		t.Fatalf("Outcome = %q, want confirmed_failed", result.Outcome)
	}
}

// T059: the upstream fully receives the message but never acknowledges
// the final dot -- the outcome must be indeterminate, never
// confirmed_failed, since the message may have actually been delivered.
func TestRelaySendIndeterminateWhenFinalAckNeverArrives(t *testing.T) {
	up := startFakeUpstream(t, fakeUpstreamOptions{HangOnData: true})
	cfg := relayConfigForUpstream(t, up)
	cfg.TimeoutSeconds = 1 // keep the test fast; the hang is deliberate

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s", TextBody: "b"})
	rewritten, err := relayRewriteMessage(raw, cfg.Identity, "agent@myapp.local")
	if err != nil {
		t.Fatalf("relayRewriteMessage failed: %v", err)
	}

	result := relaySend(cfg, cfg.Identity, []QueuedRecipient{{Address: "customer@example.com"}}, rewritten)

	if result.Outcome != outcomeIndeterminate {
		t.Fatalf("Outcome = %q, want indeterminate (reason: %s)", result.Outcome, result.FailureReason)
	}
}

// T060: an unreachable upstream must yield confirmed_failed, and must do
// so within the configured timeout rather than hanging.
func TestRelaySendDialFailureIsConfirmed(t *testing.T) {
	cfg := &RelayConfig{
		Host:           "127.0.0.1",
		TLSMode:        "starttls",
		Username:       "u",
		Password:       "p",
		Identity:       "relay@example.com",
		TimeoutSeconds: 2,
	}
	host, port, err := net.SplitHostPort(unreachableAddr(t))
	if err != nil {
		t.Fatalf("failed to split address: %v", err)
	}
	cfg.Host = host
	cfg.Port = port

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"customer@example.com"}, Subject: "s", TextBody: "b"})

	start := time.Now()
	result := relaySend(cfg, cfg.Identity, []QueuedRecipient{{Address: "customer@example.com"}}, raw)
	elapsed := time.Since(start)

	if result.Outcome != outcomeConfirmed {
		t.Fatalf("Outcome = %q, want confirmed_failed", result.Outcome)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("relaySend took %v against an unreachable host, want it bounded by TimeoutSeconds", elapsed)
	}
}

func TestRelaySendSkipsAlreadyDeliveredRecipients(t *testing.T) {
	up := startFakeUpstream(t, fakeUpstreamOptions{})
	cfg := relayConfigForUpstream(t, up)

	raw := buildTestMessage(t, testMessageOptions{From: "agent@myapp.local", To: []string{"a@example.com", "b@example.com"}, Subject: "s", TextBody: "b"})
	rewritten, err := relayRewriteMessage(raw, cfg.Identity, "agent@myapp.local")
	if err != nil {
		t.Fatalf("relayRewriteMessage failed: %v", err)
	}

	recipients := []QueuedRecipient{
		{Address: "a@example.com", Delivered: true},
		{Address: "b@example.com", Delivered: false},
	}
	result := relaySend(cfg, cfg.Identity, recipients, rewritten)

	if result.Outcome != outcomeSent {
		t.Fatalf("Outcome = %q, want sent (reason: %s)", result.Outcome, result.FailureReason)
	}
	if len(result.Recipients) != 1 || result.Recipients[0].Address != "b@example.com" {
		t.Fatalf("Recipients = %+v, want only the undelivered recipient b@example.com attempted", result.Recipients)
	}
}
