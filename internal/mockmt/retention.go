package mockmt

import (
	"fmt"
	"log"
	"time"
)

// PurgeExpiredMessages nulls the raw content of terminal (sent/rejected)
// messages whose decision is older than retentionDays, leaving metadata,
// recipients, delivery attempts, and audit events untouched (FR-033,
// FR-034) -- so FR-031/FR-030's guarantees keep holding after a purge.
// Messages not in a terminal state are never purged regardless of age
// (FR-035): pending review, sending, or unresolved failed messages are
// unreachable by this query's state filter. retentionDays <= 0 disables
// purging entirely, which is the default (FR-033).
func PurgeExpiredMessages(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	res, err := db.Exec(`
		UPDATE queued_messages
		SET raw_message = NULL, purged_at = CURRENT_TIMESTAMP
		WHERE state IN ('sent', 'rejected')
		  AND purged_at IS NULL
		  AND decided_at IS NOT NULL
		  AND decided_at < datetime('now', ?)
	`, fmt.Sprintf("-%d days", retentionDays))
	if err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// StartRetentionTicker runs PurgeExpiredMessages once immediately (so a
// short-lived container still purges) and then once a day for as long as
// the process runs. A no-op when retentionDays <= 0.
func StartRetentionTicker(retentionDays int) {
	if retentionDays <= 0 {
		return
	}

	sweep := func() {
		n, err := PurgeExpiredMessages(retentionDays)
		if err != nil {
			log.Printf("Retention sweep failed: %v", err)
			return
		}
		if n > 0 {
			log.Printf("Retention sweep: purged content for %d message(s)", n)
		}
	}

	sweep()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			sweep()
		}
	}()
}
