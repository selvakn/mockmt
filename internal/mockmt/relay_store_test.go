package mockmt

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
)

func countAuditEvents(t *testing.T, messageID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE message_id = ?", messageID).Scan(&count); err != nil {
		t.Fatalf("failed to count audit events: %v", err)
	}
	return count
}

func countQueuedRecipients(t *testing.T, messageID int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM queued_recipients WHERE message_id = ?", messageID).Scan(&count); err != nil {
		t.Fatalf("failed to count queued recipients: %v", err)
	}
	return count
}

// T030: enqueueing writes the message, every envelope recipient, and the
// initial audit event in one transaction.
func TestInsertQueuedMessageWritesMessageRecipientsAndAudit(t *testing.T) {
	setupTestDB(t)

	id, err := insertQueuedMessage("agent@myapp.local", "Agent <agent@myapp.local>", "Hello", []byte("raw bytes"), false, []queuedRecipientInput{
		{Address: "customer@example.com", Hidden: false},
		{Address: "audit@example.com", Hidden: true},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "pending_review" {
		t.Errorf("State = %q, want pending_review", msg.State)
	}
	if msg.EnvelopeFrom != "agent@myapp.local" {
		t.Errorf("EnvelopeFrom = %q, want agent@myapp.local", msg.EnvelopeFrom)
	}
	if len(msg.Recipients) != 2 {
		t.Fatalf("got %d recipients, want 2", len(msg.Recipients))
	}

	if got := countQueuedRecipients(t, id); got != 2 {
		t.Errorf("countQueuedRecipients = %d, want 2", got)
	}
	if got := countAuditEvents(t, id); got != 1 {
		t.Errorf("countAuditEvents = %d, want 1 (the initial enqueue event)", got)
	}
}

// T030: a failed insert must leave nothing behind. A recipient insert
// failing partway through insertQueuedMessage's transaction must not
// leave an orphaned message row -- exercised directly against relayTx
// since forcing a genuine mid-transaction failure inside
// insertQueuedMessage itself would require corrupting the schema.
func TestRelayTxRollsBackEverythingOnError(t *testing.T) {
	setupTestDB(t)

	err := relayTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO queued_messages (message_id, envelope_from, subject, size_bytes, state)
			VALUES (?, ?, ?, ?, 'pending_review')
		`, "rollback-test@localhost", "agent@myapp.local", "test", 0); err != nil {
			return err
		}
		return errors.New("forced failure after insert")
	})
	if err == nil {
		t.Fatal("expected relayTx to return the forced error")
	}

	if got := countQueuedMessages(t); got != 0 {
		t.Fatalf("expected the transaction to roll back entirely, but found %d queued_messages rows", got)
	}
}

// T031, SC-005: concurrent claims of the same message must produce
// exactly one winner.
func TestTryClaimMessageIsExactlyOnceUnderConcurrency(t *testing.T) {
	setupTestDB(t)

	id, err := insertQueuedMessage("agent@myapp.local", "", "Hello", []byte("raw"), false, []queuedRecipientInput{
		{Address: "customer@example.com", Hidden: false},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	const attempts = 20
	var wg sync.WaitGroup
	results := make([]bool, attempts)

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claimed, _, err := tryClaimMessage(id, []string{"pending_review"}, "reviewer@example.com")
			if err != nil {
				t.Errorf("tryClaimMessage failed: %v", err)
				return
			}
			results[i] = claimed
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, r := range results {
		if r {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("got %d winning claims out of %d concurrent attempts, want exactly 1", wins, attempts)
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "sending" {
		t.Errorf("State = %q, want sending", msg.State)
	}

	// Exactly one audit event for the pending_review -> sending
	// transition, regardless of how many callers raced for it.
	if got := countAuditEvents(t, id); got != 2 { // enqueue + claim
		t.Errorf("countAuditEvents = %d, want 2 (enqueue + exactly one claim)", got)
	}
}

// T061, FR-028: a message left in "sending" by a crashed process is
// settled by the startup sweep as Failed-indeterminate, with a
// system-attributed audit event, so a reviewer is never blocked by a
// stuck message and is never told a possibly-delivered message simply
// failed.
func TestSweepOrphanedSendingMessagesSettlesAsIndeterminate(t *testing.T) {
	setupTestDB(t)

	id, err := insertQueuedMessage("agent@myapp.local", "", "s", []byte("raw"), false, []queuedRecipientInput{
		{Address: "customer@example.com"},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	claimed, _, _, err := tryClaimMessageForSend(id, "alice@example.com", false)
	if err != nil || !claimed {
		t.Fatalf("failed to claim message for setup: claimed=%v err=%v", claimed, err)
	}

	// Simulate a second, unrelated message that is not stuck, to confirm
	// the sweep only touches orphaned ones.
	otherID, err := insertQueuedMessage("agent@myapp.local", "", "s2", []byte("raw2"), false, []queuedRecipientInput{
		{Address: "someone@example.com"},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	settled, err := SweepOrphanedSendingMessages()
	if err != nil {
		t.Fatalf("SweepOrphanedSendingMessages failed: %v", err)
	}
	if settled != 1 {
		t.Fatalf("settled = %d, want 1", settled)
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.State != "failed" || msg.FailureKind != "indeterminate" {
		t.Fatalf("state=%q failureKind=%q, want failed/indeterminate", msg.State, msg.FailureKind)
	}

	other, err := getQueuedMessage(otherID)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if other.State != "pending_review" {
		t.Fatalf("unrelated message state = %q, want pending_review (sweep must not touch it)", other.State)
	}

	// Find the sending -> failed audit event and confirm it is
	// system-attributed.
	rows, err := db.Query(`SELECT actor, to_state FROM audit_events WHERE message_id = ? ORDER BY id`, id)
	if err != nil {
		t.Fatalf("failed to query audit events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	foundSystemFailure := false
	for rows.Next() {
		var actor, toState string
		if err := rows.Scan(&actor, &toState); err != nil {
			t.Fatalf("failed to scan audit event: %v", err)
		}
		if toState == "failed" && actor == "system" {
			foundSystemFailure = true
		}
	}
	if !foundSystemFailure {
		t.Error("expected a system-attributed audit event for the sending -> failed transition")
	}
}

// T070, FR-030, FR-031: every transition produces an audit event with an
// actor and timestamp, and the trail survives the message's content
// being purged.
func TestAuditTrailRecordsTransitionsAndSurvivesPurge(t *testing.T) {
	setupTestDB(t)

	id, err := insertQueuedMessage("agent@myapp.local", "", "s", []byte("raw"), false, []queuedRecipientInput{
		{Address: "customer@example.com"},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	claimed, err := func() (bool, error) {
		c, _, _, err := tryClaimMessageForSend(id, "alice@example.com", false)
		return c, err
	}()
	if err != nil || !claimed {
		t.Fatalf("failed to claim: claimed=%v err=%v", claimed, err)
	}
	if err := markSent(id, "alice@example.com", "250 OK", []recipientOutcome{
		{Address: "customer@example.com", Delivered: true, UpstreamResponse: "250 OK"},
	}); err != nil {
		t.Fatalf("markSent failed: %v", err)
	}

	events, _, err := getAuditTrail(id)
	if err != nil {
		t.Fatalf("getAuditTrail failed: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d audit events, want 3 (enqueue, pending_review->sending, sending->sent)", len(events))
	}
	for _, e := range events {
		if e.Actor == "" {
			t.Error("audit event missing an actor")
		}
		if e.OccurredAt.IsZero() {
			t.Error("audit event missing a timestamp")
		}
	}
	if events[len(events)-1].ToState != "sent" || events[len(events)-1].Actor != "alice@example.com" {
		t.Errorf("final event = %+v, want to_state=sent actor=alice@example.com", events[len(events)-1])
	}

	// Simulate what retention purging (Phase 8) does: null the content,
	// leave everything else alone.
	if _, err := db.Exec(`UPDATE queued_messages SET raw_message = NULL, purged_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		t.Fatalf("failed to simulate purge: %v", err)
	}

	eventsAfterPurge, attemptsAfterPurge, err := getAuditTrail(id)
	if err != nil {
		t.Fatalf("getAuditTrail after purge failed: %v", err)
	}
	if len(eventsAfterPurge) != len(events) {
		t.Fatalf("audit trail changed size after purge: got %d, want %d", len(eventsAfterPurge), len(events))
	}
	_ = attemptsAfterPurge
}

func TestGetAuditTrailReturnsErrNoRowsForUnknownMessage(t *testing.T) {
	setupTestDB(t)

	_, _, err := getAuditTrail(999999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

// T035, FR-018a, SC-010: queueing and relaying to external recipients
// must never create a portal user account for them.
func TestQueueingNeverCreatesUserAccounts(t *testing.T) {
	setupTestDB(t)

	before := countUsers(t)

	_, err := insertQueuedMessage("agent@myapp.local", "", "Hello", []byte("raw"), false, []queuedRecipientInput{
		{Address: "customer@example.com", Hidden: false},
		{Address: "audit@example.com", Hidden: true},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	after := countUsers(t)
	if after != before {
		t.Fatalf("countUsers changed from %d to %d: relaying must never provision a portal account for an external recipient", before, after)
	}
}
