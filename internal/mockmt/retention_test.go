package mockmt

import (
	"strconv"
	"testing"
)

func insertTerminalMessageAt(t *testing.T, state string, decidedAtOffsetDays int) int64 {
	t.Helper()

	id, err := insertQueuedMessage("agent@myapp.local", "", "s", []byte("raw"), false, []queuedRecipientInput{
		{Address: "customer@example.com"},
	})
	if err != nil {
		t.Fatalf("insertQueuedMessage failed: %v", err)
	}

	_, err = db.Exec(`
		UPDATE queued_messages
		SET state = ?, decided_at = datetime('now', ?), decided_by = 'alice@example.com'
		WHERE id = ?
	`, state, formatDayOffset(decidedAtOffsetDays), id)
	if err != nil {
		t.Fatalf("failed to backdate message: %v", err)
	}
	return id
}

func formatDayOffset(days int) string {
	if days <= 0 {
		return "+0 days"
	}
	return "-" + strconv.Itoa(days) + " days"
}

// T075, FR-033/FR-034: a terminal message older than the retention
// period has its content purged, while metadata, recipients, and audit
// records survive.
func TestPurgeExpiredMessagesPurgesOldTerminalMessages(t *testing.T) {
	setupTestDB(t)

	oldSent := insertTerminalMessageAt(t, "sent", 100)
	oldRejected := insertTerminalMessageAt(t, "rejected", 100)

	n, err := PurgeExpiredMessages(90)
	if err != nil {
		t.Fatalf("PurgeExpiredMessages failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d messages, want 2", n)
	}

	for _, id := range []int64{oldSent, oldRejected} {
		msg, err := getQueuedMessage(id)
		if err != nil {
			t.Fatalf("getQueuedMessage failed: %v", err)
		}
		if msg.RawMessage != nil {
			t.Errorf("message %d: RawMessage not nil after purge", id)
		}
		if msg.PurgedAt == nil {
			t.Errorf("message %d: PurgedAt not set after purge", id)
		}
		if msg.EnvelopeFrom == "" || msg.Subject == "" {
			t.Errorf("message %d: metadata lost after purge", id)
		}
		if len(msg.Recipients) == 0 {
			t.Errorf("message %d: recipients lost after purge", id)
		}
		events, _, err := getAuditTrail(id)
		if err != nil || len(events) == 0 {
			t.Errorf("message %d: audit trail lost after purge (err=%v)", id, err)
		}
	}
}

// FR-035: pending, sending, and unresolved failed messages are never
// purged regardless of age.
func TestPurgeExpiredMessagesNeverTouchesNonTerminalStates(t *testing.T) {
	setupTestDB(t)

	pendingID := insertTerminalMessageAt(t, "pending_review", 1000)
	sendingID := insertTerminalMessageAt(t, "sending", 1000)
	failedID := insertTerminalMessageAt(t, "failed", 1000)

	n, err := PurgeExpiredMessages(1)
	if err != nil {
		t.Fatalf("PurgeExpiredMessages failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("purged %d messages, want 0 (none are terminal)", n)
	}

	for _, id := range []int64{pendingID, sendingID, failedID} {
		msg, err := getQueuedMessage(id)
		if err != nil {
			t.Fatalf("getQueuedMessage failed: %v", err)
		}
		if msg.RawMessage == nil {
			t.Errorf("message %d: content purged despite being non-terminal (state=%s)", id, msg.State)
		}
	}
}

// A terminal message younger than the retention period must not be
// purged yet.
func TestPurgeExpiredMessagesSparesRecentTerminalMessages(t *testing.T) {
	setupTestDB(t)

	recentID := insertTerminalMessageAt(t, "sent", 5)

	n, err := PurgeExpiredMessages(90)
	if err != nil {
		t.Fatalf("PurgeExpiredMessages failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("purged %d messages, want 0 (too recent)", n)
	}

	msg, err := getQueuedMessage(recentID)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.RawMessage == nil {
		t.Error("content purged despite being within the retention window")
	}
}

func TestPurgeExpiredMessagesDisabledByZeroRetention(t *testing.T) {
	setupTestDB(t)

	insertTerminalMessageAt(t, "sent", 10000)

	n, err := PurgeExpiredMessages(0)
	if err != nil {
		t.Fatalf("PurgeExpiredMessages failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("purged %d messages with retention disabled, want 0", n)
	}
}

// A purged message must not be retriable: markFailed/markSent require
// state='sending', which a purged sent/rejected message can never be in
// again without going through a fresh claim -- and the send endpoint
// itself checks PurgedAt (T077). This test confirms the store-level
// invariant that a purged message's raw content, once gone, cannot be
// recovered by re-reading it.
func TestPurgedMessageContentIsUnrecoverable(t *testing.T) {
	setupTestDB(t)

	id := insertTerminalMessageAt(t, "sent", 100)
	if _, err := PurgeExpiredMessages(90); err != nil {
		t.Fatalf("PurgeExpiredMessages failed: %v", err)
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		t.Fatalf("getQueuedMessage failed: %v", err)
	}
	if msg.RawMessage != nil {
		t.Fatal("expected RawMessage to be nil after purge")
	}
}
