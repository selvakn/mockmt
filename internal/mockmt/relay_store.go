package mockmt

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// QueuedMessage is a relay-mode message and its review/delivery state.
// Deliberately has no owning user (data-model.md): recipients are
// external parties, not portal identities (FR-018a).
type QueuedMessage struct {
	ID               int64
	MessageID        string
	EnvelopeFrom     string
	HeaderFrom       string
	Subject          string
	RawMessage       []byte // nil once purged (FR-036)
	SizeBytes        int64
	State            string
	FailureKind      string
	FailureReason    string
	UpstreamResponse string
	ReceivedAt       time.Time
	DecidedAt        *time.Time
	DecidedBy        string
	PurgedAt         *time.Time
	Recipients       []QueuedRecipient
}

// QueuedRecipient is one envelope recipient of a QueuedMessage (FR-015a).
type QueuedRecipient struct {
	ID               int64
	MessageID        int64
	Address          string
	Hidden           bool
	Delivered        bool
	DeliveredAt      *time.Time
	UpstreamResponse string
}

// queuedRecipientInput is what insertQueuedMessage needs per recipient at
// ingest time -- before any delivery has been attempted.
type queuedRecipientInput struct {
	Address string
	Hidden  bool
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// relayTx runs fn inside a transaction, committing on success and rolling
// back on any returned error or panic. Every state change and its audit
// event are written this way (data-model.md), so the two can never
// diverge -- an audit trail that lagged or lost writes would defeat
// FR-030.
func relayTx(fn func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

// appendAuditEvent records one state transition. fromState is empty for
// the initial enqueue, which has no prior state. Callers must invoke this
// inside the same transaction as the change it records (FR-030); it is
// never called standalone.
func appendAuditEvent(tx *sql.Tx, messageID int64, fromState, toState, actor, detail string) error {
	var fromStateVal, detailVal interface{}
	if fromState != "" {
		fromStateVal = fromState
	}
	if detail != "" {
		detailVal = detail
	}

	_, err := tx.Exec(`
		INSERT INTO audit_events (message_id, from_state, to_state, actor, detail)
		VALUES (?, ?, ?, ?, ?)
	`, messageID, fromStateVal, toState, actor, detailVal)
	return err
}

// insertQueuedMessage stores a newly accepted message, its envelope
// recipients, and the initial audit event in a single transaction
// (FR-009): a submission that fails partway leaves nothing behind, and
// once this returns successfully the submission is never lost. One
// message row regardless of recipient count (FR-021a).
func insertQueuedMessage(envelopeFrom, headerFrom, subject string, raw []byte, recipients []queuedRecipientInput) (int64, error) {
	messageID := generateMessageID()

	var id int64
	err := relayTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			INSERT INTO queued_messages (message_id, envelope_from, header_from, subject, raw_message, size_bytes, state)
			VALUES (?, ?, ?, ?, ?, ?, 'pending_review')
		`, messageID, envelopeFrom, nullableString(headerFrom), subject, raw, len(raw))
		if err != nil {
			return err
		}

		id, err = res.LastInsertId()
		if err != nil {
			return err
		}

		for _, r := range recipients {
			if _, err := tx.Exec(`
				INSERT INTO queued_recipients (message_id, address, hidden)
				VALUES (?, ?, ?)
			`, id, r.Address, r.Hidden); err != nil {
				return err
			}
		}

		return appendAuditEvent(tx, id, "", "pending_review", "system", fmt.Sprintf("accepted from %s", envelopeFrom))
	})

	return id, err
}

// loadRecipients returns messageID's envelope recipients in insertion
// order.
func loadRecipients(messageID int64) ([]QueuedRecipient, error) {
	rows, err := db.Query(`
		SELECT id, message_id, address, hidden, delivered, delivered_at, upstream_response
		FROM queued_recipients WHERE message_id = ? ORDER BY id
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var recipients []QueuedRecipient
	for rows.Next() {
		var r QueuedRecipient
		var deliveredAt sql.NullTime
		var upstreamResponse sql.NullString
		if err := rows.Scan(&r.ID, &r.MessageID, &r.Address, &r.Hidden, &r.Delivered, &deliveredAt, &upstreamResponse); err != nil {
			return nil, err
		}
		r.UpstreamResponse = upstreamResponse.String
		if deliveredAt.Valid {
			t := deliveredAt.Time
			r.DeliveredAt = &t
		}
		recipients = append(recipients, r)
	}
	return recipients, rows.Err()
}

// getQueuedMessage returns one message, including its raw bytes (nil if
// purged) and its envelope recipients.
func getQueuedMessage(id int64) (*QueuedMessage, error) {
	var m QueuedMessage
	var headerFrom, failureKind, failureReason, upstreamResponse, decidedBy sql.NullString
	var decidedAt, purgedAt sql.NullTime

	err := db.QueryRow(`
		SELECT id, message_id, envelope_from, header_from, subject, raw_message, size_bytes, state,
		       failure_kind, failure_reason, upstream_response, received_at, decided_at, decided_by, purged_at
		FROM queued_messages WHERE id = ?
	`, id).Scan(&m.ID, &m.MessageID, &m.EnvelopeFrom, &headerFrom, &m.Subject, &m.RawMessage, &m.SizeBytes, &m.State,
		&failureKind, &failureReason, &upstreamResponse, &m.ReceivedAt, &decidedAt, &decidedBy, &purgedAt)
	if err != nil {
		return nil, err
	}

	m.HeaderFrom = headerFrom.String
	m.FailureKind = failureKind.String
	m.FailureReason = failureReason.String
	m.UpstreamResponse = upstreamResponse.String
	m.DecidedBy = decidedBy.String
	if decidedAt.Valid {
		t := decidedAt.Time
		m.DecidedAt = &t
	}
	if purgedAt.Valid {
		t := purgedAt.Time
		m.PurgedAt = &t
	}

	recipients, err := loadRecipients(id)
	if err != nil {
		return nil, err
	}
	m.Recipients = recipients

	return &m, nil
}

// listQueue returns a page of queued messages -- filtered by state unless
// state is "" or "all" -- ordered newest first, along with the total
// matching count for pagination (FR-019, SC-008).
func listQueue(state string, limit, offset int) (total int, messages []QueuedMessage, err error) {
	where := ""
	countArgs := []interface{}{}
	if state != "" && state != "all" {
		where = "WHERE state = ?"
		countArgs = append(countArgs, state)
	}

	if err = db.QueryRow("SELECT COUNT(*) FROM queued_messages "+where, countArgs...).Scan(&total); err != nil {
		return 0, nil, err
	}

	queryArgs := append(append([]interface{}{}, countArgs...), limit, offset)
	rows, err := db.Query(`
		SELECT id, message_id, envelope_from, header_from, subject, size_bytes, state,
		       failure_kind, failure_reason, received_at, decided_at, decided_by, purged_at
		FROM queued_messages `+where+`
		ORDER BY received_at DESC
		LIMIT ? OFFSET ?
	`, queryArgs...)
	if err != nil {
		return 0, nil, err
	}

	for rows.Next() {
		var m QueuedMessage
		var headerFrom, failureKind, failureReason, decidedBy sql.NullString
		var decidedAt, purgedAt sql.NullTime
		if err := rows.Scan(&m.ID, &m.MessageID, &m.EnvelopeFrom, &headerFrom, &m.Subject, &m.SizeBytes, &m.State,
			&failureKind, &failureReason, &m.ReceivedAt, &decidedAt, &decidedBy, &purgedAt); err != nil {
			_ = rows.Close()
			return 0, nil, err
		}
		m.HeaderFrom = headerFrom.String
		m.FailureKind = failureKind.String
		m.FailureReason = failureReason.String
		m.DecidedBy = decidedBy.String
		if decidedAt.Valid {
			t := decidedAt.Time
			m.DecidedAt = &t
		}
		if purgedAt.Valid {
			t := purgedAt.Time
			m.PurgedAt = &t
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, nil, err
	}
	_ = rows.Close()

	for i := range messages {
		recipients, err := loadRecipients(messages[i].ID)
		if err != nil {
			return 0, nil, err
		}
		messages[i].Recipients = recipients
	}

	return total, messages, nil
}

// tryClaimMessage attempts to move message id into the sending state from
// any of fromStates, via a conditional UPDATE whose affected-row count
// elects the winner (research R6) -- never a read-then-write, which would
// leave a race window. This is the only code path that writes the
// sending state, so every send attempt (first send or retry) funnels
// through it, which is what makes FR-022's at-most-once guarantee hold
// under concurrent or repeated callers.
func tryClaimMessage(id int64, fromStates []string, actor string) (claimed bool, previousState string, err error) {
	err = relayTx(func(tx *sql.Tx) error {
		if scanErr := tx.QueryRow(`SELECT state FROM queued_messages WHERE id = ?`, id).Scan(&previousState); scanErr != nil {
			return scanErr
		}

		placeholders := make([]string, len(fromStates))
		args := make([]interface{}, 0, len(fromStates)+1)
		args = append(args, id)
		for i, s := range fromStates {
			placeholders[i] = "?"
			args = append(args, s)
		}

		query := fmt.Sprintf(`UPDATE queued_messages SET state = 'sending' WHERE id = ? AND state IN (%s)`, strings.Join(placeholders, ","))
		res, execErr := tx.Exec(query, args...)
		if execErr != nil {
			return execErr
		}

		n, raErr := res.RowsAffected()
		if raErr != nil {
			return raErr
		}
		claimed = n == 1
		if !claimed {
			return nil
		}

		return appendAuditEvent(tx, id, previousState, "sending", actor, "")
	})
	return claimed, previousState, err
}

// AuditEvent is one immutable state-transition record (FR-030).
type AuditEvent struct {
	FromState  string
	ToState    string
	Actor      string
	OccurredAt time.Time
	Detail     string
}

// DeliveryAttempt is one try at relaying a message (FR-025).
type DeliveryAttempt struct {
	StartedAt        time.Time
	FinishedAt       *time.Time
	Outcome          string
	UpstreamResponse string
	Error            string
	InitiatedBy      string
}

// getAuditTrail returns messageID's full state-change history and every
// delivery attempt across any retries (FR-030). Available even for a
// purged message: audit records outlive content (FR-031). Returns
// sql.ErrNoRows if the message never existed at all, so a query against
// a genuinely unknown ID is distinguishable from one with an empty
// (but real) history.
func getAuditTrail(messageID int64) ([]AuditEvent, []DeliveryAttempt, error) {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM queued_messages WHERE id = ?`, messageID).Scan(&exists); err != nil {
		return nil, nil, err
	}
	if exists == 0 {
		return nil, nil, sql.ErrNoRows
	}

	eventRows, err := db.Query(`
		SELECT from_state, to_state, actor, occurred_at, detail
		FROM audit_events WHERE message_id = ? ORDER BY id
	`, messageID)
	if err != nil {
		return nil, nil, err
	}
	var events []AuditEvent
	for eventRows.Next() {
		var e AuditEvent
		var fromState, detail sql.NullString
		if err := eventRows.Scan(&fromState, &e.ToState, &e.Actor, &e.OccurredAt, &detail); err != nil {
			_ = eventRows.Close()
			return nil, nil, err
		}
		e.FromState = fromState.String
		e.Detail = detail.String
		events = append(events, e)
	}
	if err := eventRows.Err(); err != nil {
		_ = eventRows.Close()
		return nil, nil, err
	}
	_ = eventRows.Close()

	attemptRows, err := db.Query(`
		SELECT started_at, finished_at, outcome, upstream_response, error, initiated_by
		FROM delivery_attempts WHERE message_id = ? ORDER BY id
	`, messageID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = attemptRows.Close() }()

	var attempts []DeliveryAttempt
	for attemptRows.Next() {
		var a DeliveryAttempt
		var finishedAt sql.NullTime
		var outcome, upstreamResponse, errText sql.NullString
		if err := attemptRows.Scan(&a.StartedAt, &finishedAt, &outcome, &upstreamResponse, &errText, &a.InitiatedBy); err != nil {
			return nil, nil, err
		}
		if finishedAt.Valid {
			t := finishedAt.Time
			a.FinishedAt = &t
		}
		a.Outcome = outcome.String
		a.UpstreamResponse = upstreamResponse.String
		a.Error = errText.String
		attempts = append(attempts, a)
	}
	return events, attempts, attemptRows.Err()
}

// SweepOrphanedSendingMessages settles any message left in "sending" from
// a previous process -- a crash mid-send -- as Failed-indeterminate, with
// a system-attributed audit event (FR-028). Called once at startup,
// before serving, so a reviewer is never blocked by a message stuck in
// sending and is never told a possibly-delivered message simply failed.
func SweepOrphanedSendingMessages() (int, error) {
	rows, err := db.Query(`SELECT id FROM queued_messages WHERE state = 'sending'`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	const reason = "process restarted while this message was being sent; delivery outcome is unknown"
	settled := 0
	for _, id := range ids {
		if err := markFailed(id, "indeterminate", reason, "system", nil); err != nil {
			log.Printf("startup sweep: failed to settle message %d: %v", id, err)
			continue
		}
		settled++
	}
	return settled, nil
}

// tryClaimMessageForSend attempts to move message id into sending for a
// first send or a retry. Unlike a plain state check, the
// indeterminate-retry guard (FR-025a) is encoded directly inside the
// conditional UPDATE, in the same transaction as the state read that
// decides it -- re-checking the guard in a separate, later transaction
// would leave a window where a different reviewer's concurrent attempt
// could turn the message into a failed-indeterminate one after this
// caller had already decided confirmation was unnecessary. Because the
// database is opened with _txlock=immediate, this transaction holds the
// write lock from its first statement, so no concurrent writer can
// interleave between the read and the update.
//
// Returns exactly one of: claimed (success), needsConfirmation (a
// failed-indeterminate retry without confirmDuplicateRisk), or neither
// (already handled by someone else -- inspect previousState).
func tryClaimMessageForSend(id int64, actor string, confirmDuplicateRisk bool) (claimed, needsConfirmation bool, previousState string, err error) {
	err = relayTx(func(tx *sql.Tx) error {
		var failureKind sql.NullString
		if scanErr := tx.QueryRow(`SELECT state, failure_kind FROM queued_messages WHERE id = ?`, id).Scan(&previousState, &failureKind); scanErr != nil {
			return scanErr
		}

		if previousState == "failed" && failureKind.String == "indeterminate" && !confirmDuplicateRisk {
			needsConfirmation = true
			return nil
		}

		res, execErr := tx.Exec(`
			UPDATE queued_messages
			SET state = 'sending'
			WHERE id = ?
			  AND (
				state = 'pending_review'
				OR (state = 'failed' AND (failure_kind != 'indeterminate' OR ?))
			  )
		`, id, confirmDuplicateRisk)
		if execErr != nil {
			return execErr
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return raErr
		}
		claimed = n == 1
		if !claimed {
			return nil
		}
		return appendAuditEvent(tx, id, previousState, "sending", actor, "")
	})
	return claimed, needsConfirmation, previousState, err
}

// markSent settles a claimed message as delivered (FR-023): records the
// delivery timestamp, the approving reviewer, and the upstream acceptance
// response, and marks each successfully served recipient. Done in one
// transaction so the message and its per-recipient results can never
// disagree.
func markSent(id int64, reviewer, upstreamResponse string, deliveredAddresses []string) error {
	return relayTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE queued_messages
			SET state = 'sent', decided_at = CURRENT_TIMESTAMP, decided_by = ?, upstream_response = ?
			WHERE id = ? AND state = 'sending'
		`, reviewer, upstreamResponse, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("markSent: message %d was not in sending state", id)
		}

		for _, addr := range deliveredAddresses {
			if _, err := tx.Exec(`
				UPDATE queued_recipients
				SET delivered = TRUE, delivered_at = CURRENT_TIMESTAMP
				WHERE message_id = ? AND address = ?
			`, id, addr); err != nil {
				return err
			}
		}

		return appendAuditEvent(tx, id, "sending", "sent", reviewer, upstreamResponse)
	})
}

// markFailed settles a claimed message as failed (FR-024): records the
// failure kind and reason, retains the full message content, marks
// whichever recipients were actually served before the failure, and
// remains eligible for retry. actor is the reviewer whose Send Now
// triggered the attempt that failed -- the failure itself wasn't their
// choice, but the attempt was, and FR-030 wants the actor who caused the
// transition.
func markFailed(id int64, kind, reason, actor string, recipientResults []recipientOutcome) error {
	return relayTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE queued_messages
			SET state = 'failed', failure_kind = ?, failure_reason = ?
			WHERE id = ? AND state = 'sending'
		`, kind, reason, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("markFailed: message %d was not in sending state", id)
		}

		for _, r := range recipientResults {
			if !r.Delivered {
				continue
			}
			if _, err := tx.Exec(`
				UPDATE queued_recipients
				SET delivered = TRUE, delivered_at = CURRENT_TIMESTAMP, upstream_response = ?
				WHERE message_id = ? AND address = ?
			`, r.UpstreamResponse, id, r.Address); err != nil {
				return err
			}
		}

		return appendAuditEvent(tx, id, "sending", "failed", actor, reason)
	})
}

// rejectMessage moves a message in one of fromStates to rejected --
// pending_review for an ordinary reject (FR-026), or failed for an
// abandon (FR-026a). It will never be delivered, and the rejection --
// with its actor and timestamp -- is retained for audit even after the
// message leaves the reviewer's inbox view (FR-031). A conditional
// update, for the same reason claims are conditional: two reviewers
// racing to decide a message must not both succeed. Returns
// sql.ErrNoRows if the message does not exist at all, so callers can
// distinguish "not found" (404) from "already handled" (409).
func rejectMessage(id int64, fromStates []string, reviewer, reason string) (claimed bool, previousState string, err error) {
	err = relayTx(func(tx *sql.Tx) error {
		if scanErr := tx.QueryRow(`SELECT state FROM queued_messages WHERE id = ?`, id).Scan(&previousState); scanErr != nil {
			return scanErr
		}

		placeholders := make([]string, len(fromStates))
		args := make([]interface{}, 0, len(fromStates)+2)
		args = append(args, reviewer, id)
		for i, s := range fromStates {
			placeholders[i] = "?"
			args = append(args, s)
		}

		query := fmt.Sprintf(`
			UPDATE queued_messages
			SET state = 'rejected', decided_at = CURRENT_TIMESTAMP, decided_by = ?
			WHERE id = ? AND state IN (%s)
		`, strings.Join(placeholders, ","))
		res, execErr := tx.Exec(query, args...)
		if execErr != nil {
			return execErr
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return raErr
		}
		claimed = n == 1
		if !claimed {
			return nil
		}
		return appendAuditEvent(tx, id, previousState, "rejected", reviewer, reason)
	})
	return claimed, previousState, err
}

// startDeliveryAttempt records that an attempt is beginning, before the
// (potentially slow) network conversation with the upstream, so even a
// crash mid-send leaves a discoverable attempt row for the startup sweep
// to settle (FR-028).
func startDeliveryAttempt(id int64, initiatedBy string) (int64, error) {
	res, err := db.Exec(`INSERT INTO delivery_attempts (message_id, initiated_by) VALUES (?, ?)`, id, initiatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// finishDeliveryAttempt records one attempt's outcome (FR-025: each
// attempt across retries is recorded separately). Called after relaySend
// returns, regardless of outcome -- deliberately not wrapped in the same
// transaction as markSent/markFailed, since it brackets a slow network
// call that must not hold a database lock open.
func finishDeliveryAttempt(attemptID int64, outcome deliveryOutcome, upstreamResponse, errText string) error {
	_, err := db.Exec(`
		UPDATE delivery_attempts
		SET finished_at = CURRENT_TIMESTAMP, outcome = ?, upstream_response = ?, error = ?
		WHERE id = ?
	`, string(outcome), nullableString(upstreamResponse), nullableString(errText), attemptID)
	return err
}
