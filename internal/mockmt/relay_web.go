package mockmt

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// requireReviewer restricts access to authorized reviewers (FR-017): a
// portal user whose authenticated email appears in the operator-configured
// reviewer list. Must run after authMiddleware(), which sets user_email.
// Resolved fresh from relayCfg on every request, never cached in the JWT,
// so removing a reviewer takes effect on their very next request rather
// than when a 24-hour token expires.
func requireReviewer() gin.HandlerFunc {
	return func(c *gin.Context) {
		email := c.GetString("user_email")
		if relayCfg == nil || !relayCfg.IsReviewer(email) {
			c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// relayModeGate hides the relay API entirely when relay mode is disabled
// (FR-002): every gated path responds 404, not merely an empty result, so
// the feature is absent from the API surface rather than just hidden in
// the UI. GET /api/relay/status is deliberately registered outside this
// gate, since the portal needs it to discover the mode in the first
// place.
func relayModeGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if relayCfg == nil || !relayCfg.Enabled {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// handleRelayStatus reports the operating mode and whether the caller may
// act as a reviewer (FR-005). Exempt from both relayModeGate and
// requireReviewer: it is how the portal discovers the mode in the first
// place, and it discloses nothing sensitive -- never host, port, or
// credentials (FR-006).
func handleRelayStatus(c *gin.Context) {
	if relayCfg == nil || !relayCfg.Enabled {
		c.JSON(http.StatusOK, gin.H{"relay_enabled": false, "is_reviewer": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"relay_enabled":  true,
		"is_reviewer":    relayCfg.IsReviewer(c.GetString("user_email")),
		"relay_identity": relayCfg.Identity,
	})
}

const (
	defaultQueueLimit = 50
	maxQueueLimit     = 200
)

// recipientJSON is the wire shape of one envelope recipient (FR-015a).
type recipientJSON struct {
	Address          string  `json:"address"`
	Hidden           bool    `json:"hidden"`
	Delivered        bool    `json:"delivered"`
	UpstreamResponse *string `json:"upstream_response"`
}

func recipientsJSON(recipients []QueuedRecipient) []recipientJSON {
	out := make([]recipientJSON, len(recipients))
	for i, r := range recipients {
		out[i] = recipientJSON{Address: r.Address, Hidden: r.Hidden, Delivered: r.Delivered}
		if r.UpstreamResponse != "" {
			resp := r.UpstreamResponse
			out[i].UpstreamResponse = &resp
		}
	}
	return out
}

// handleRelayQueue lists queued messages, filtered by state and paginated
// (FR-014, FR-015, FR-019, SC-008). The recipient list shown is always
// the envelope list, with hidden (blind-carbon) addresses flagged
// (FR-015a).
func handleRelayQueue(c *gin.Context) {
	state := c.DefaultQuery("state", "pending_review")

	limit := defaultQueueLimit
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > maxQueueLimit {
		limit = maxQueueLimit
	}

	offset := 0
	if raw := c.Query("offset"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			offset = v
		}
	}

	total, messages, err := listQueue(state, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list queue"})
		return
	}

	items := make([]gin.H, len(messages))
	for i, m := range messages {
		items[i] = gin.H{
			"id":              m.ID,
			"envelope_from":   m.EnvelopeFrom,
			"subject":         m.Subject,
			"recipients":      recipientsJSON(m.Recipients),
			"recipient_count": len(m.Recipients),
			"state":           m.State,
			"received_at":     m.ReceivedAt,
			"size_bytes":      m.SizeBytes,
			"has_attachments": m.PurgedAt == nil && messageHasAttachments(m.RawMessage),
		}
	}

	c.JSON(http.StatusOK, gin.H{"total": total, "messages": items})
}

// loadQueuedMessageOr404 fetches message id, writing a 404 response and
// returning ok=false if it does not exist.
func loadQueuedMessageOr404(c *gin.Context) (*QueuedMessage, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid message id"})
		return nil, false
	}

	msg, err := getQueuedMessage(id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return nil, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load message"})
		return nil, false
	}
	return msg, true
}

// handleRelayMessageDetail returns full message content for review
// (FR-016): both bodies, the attachment list, and both the envelope and
// header sender identities (FR-013b). A purged message reports its
// metadata with purged: true and omits content (FR-036).
func handleRelayMessageDetail(c *gin.Context) {
	msg, ok := loadQueuedMessageOr404(c)
	if !ok {
		return
	}

	base := gin.H{
		"id":             msg.ID,
		"message_id":     msg.MessageID,
		"envelope_from":  msg.EnvelopeFrom,
		"header_from":    msg.HeaderFrom,
		"subject":        msg.Subject,
		"recipients":     recipientsJSON(msg.Recipients),
		"state":          msg.State,
		"failure_kind":   nullableJSONString(msg.FailureKind),
		"failure_reason": nullableJSONString(msg.FailureReason),
		"received_at":    msg.ReceivedAt,
		"decided_at":     msg.DecidedAt,
		"decided_by":     nullableJSONString(msg.DecidedBy),
	}

	if msg.PurgedAt != nil || msg.RawMessage == nil {
		base["purged"] = true
		c.JSON(http.StatusOK, base)
		return
	}
	base["purged"] = false

	meta, err := parseMessageMetadata(msg.RawMessage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse message content"})
		return
	}

	attachments := make([]gin.H, len(meta.Attachments))
	for i, a := range meta.Attachments {
		attachments[i] = gin.H{
			"index":        a.Index,
			"filename":     a.Filename,
			"content_type": a.ContentType,
			"size_bytes":   a.SizeBytes,
			"previewable":  isPreviewableContentType(a.ContentType),
		}
	}

	base["text_body"] = meta.TextBody
	base["html_body"] = meta.HTMLBody
	base["attachments"] = attachments

	c.Header("Content-Security-Policy", "default-src 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(http.StatusOK, base)
}

func nullableJSONString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// handleRelayAttachment streams one attachment for preview or download
// (FR-016a, FR-016b). The whole-message I/O slot is released as soon as
// the part is extracted, before it is written to the response, since
// only the send path needs to hold its slot for a whole conversation
// (research R16).
func handleRelayAttachment(c *gin.Context) {
	msg, ok := loadQueuedMessageOr404(c)
	if !ok {
		return
	}

	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachment index"})
		return
	}

	if msg.PurgedAt != nil || msg.RawMessage == nil {
		c.JSON(http.StatusGone, gin.H{"error": "message content was purged"})
		return
	}

	if !relayIOSem.Acquire(c.Request.Context()) {
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server busy, try again"})
		return
	}
	part, err := extractPart(msg.RawMessage, index)
	relayIOSem.Release()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "attachment not found"})
		return
	}

	contentType := part.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	disposition := "attachment"
	if c.Query("disposition") == "inline" && isPreviewableContentType(contentType) {
		disposition = "inline"
	}

	c.Header("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, part.Filename))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, contentType, part.Body)
}

// handleRelaySend approves and relays a pending message (FR-020, FR-021).
// Order matters: the I/O slot is acquired before the message is claimed,
// so a busy server never strands a message in "sending" (research R16).
// A failed delivery is still a successful request -- it returns 200 with
// the failure detail, not an HTTP error status.
type sendRequest struct {
	ConfirmDuplicateRisk bool `json:"confirm_duplicate_risk"`
}

func handleRelaySend(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid message id"})
		return
	}

	var req sendRequest
	_ = c.ShouldBindJSON(&req)

	if !relayIOSem.Acquire(c.Request.Context()) {
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "server busy, try again"})
		return
	}
	defer relayIOSem.Release()

	reviewer := c.GetString("user_email")

	claimed, needsConfirmation, previousState, err := tryClaimMessageForSend(id, reviewer, req.ConfirmDuplicateRisk)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to claim message"})
		return
	}
	if needsConfirmation {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "this message's last delivery attempt had an unknown outcome; a retry may deliver a duplicate. Set confirm_duplicate_risk to proceed.",
		})
		return
	}
	if !claimed {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("message was already handled (state: %s)", previousState)})
		return
	}

	msg, err := getQueuedMessage(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load message"})
		return
	}

	attemptID, err := startDeliveryAttempt(id, reviewer)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record delivery attempt"})
		return
	}

	rewritten, err := relayRewriteMessage(msg.RawMessage, relayCfg.Identity, msg.EnvelopeFrom)
	if err != nil {
		reason := fmt.Sprintf("failed to prepare message for relay: %v", err)
		_ = finishDeliveryAttempt(attemptID, outcomeConfirmed, "", reason)
		_ = markFailed(id, "confirmed", reason, reviewer, nil)
		c.JSON(http.StatusOK, gin.H{"state": "failed", "failure_kind": "confirmed", "failure_reason": reason})
		return
	}

	result := relaySend(relayCfg, relayCfg.Identity, msg.Recipients, rewritten)
	_ = finishDeliveryAttempt(attemptID, result.Outcome, result.UpstreamResponse, result.FailureReason)

	if result.Outcome == outcomeSent {
		var delivered []string
		for _, r := range result.Recipients {
			if r.Delivered {
				delivered = append(delivered, r.Address)
			}
		}
		if err := markSent(id, reviewer, result.UpstreamResponse, delivered); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record delivery"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"state":             "sent",
			"decided_by":        reviewer,
			"upstream_response": result.UpstreamResponse,
			"recipients":        recipientOutcomesJSON(result.Recipients),
		})
		return
	}

	failureKind := "confirmed"
	if result.Outcome == outcomeIndeterminate {
		failureKind = "indeterminate"
	}
	if err := markFailed(id, failureKind, result.FailureReason, reviewer, result.Recipients); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record failure"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"state":          "failed",
		"failure_kind":   failureKind,
		"failure_reason": result.FailureReason,
		"recipients":     recipientOutcomesJSON(result.Recipients),
	})
}

func recipientOutcomesJSON(recipients []recipientOutcome) []gin.H {
	out := make([]gin.H, len(recipients))
	for i, r := range recipients {
		out[i] = gin.H{"address": r.Address, "delivered": r.Delivered, "upstream_response": nullableJSONString(r.UpstreamResponse)}
	}
	return out
}

type rejectRequest struct {
	Reason string `json:"reason"`
}

// handleRelayReject rejects a pending message (FR-026), or abandons a
// failed one (FR-026a) -- the same action from the reviewer's point of
// view, "this must never go out." Abandoning a message whose last
// outcome was indeterminate records that its delivery status was unknown
// at the time it was abandoned (FR-026a).
func handleRelayReject(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid message id"})
		return
	}

	var req rejectRequest
	_ = c.ShouldBindJSON(&req)

	reviewer := c.GetString("user_email")

	msg, err := getQueuedMessage(id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load message"})
		return
	}

	reason := req.Reason
	if msg.State == "failed" && msg.FailureKind == "indeterminate" && reason == "" {
		reason = "abandoned while the prior delivery attempt's outcome was unknown (indeterminate)"
	} else if msg.State == "failed" && reason == "" {
		reason = "abandoned"
	}

	claimed, previousState, err := rejectMessage(id, []string{"pending_review", "failed"}, reviewer, reason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reject message"})
		return
	}
	if !claimed {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("message was already handled (state: %s)", previousState)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"state": "rejected", "decided_by": reviewer})
}

// handleRelayAudit returns the full state-change history and every
// delivery attempt for a message (FR-030), available even for a purged
// message since audit records outlive content (FR-031).
func handleRelayAudit(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid message id"})
		return
	}

	events, attempts, err := getAuditTrail(id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load audit trail"})
		return
	}

	eventsJSON := make([]gin.H, len(events))
	for i, e := range events {
		eventsJSON[i] = gin.H{
			"from_state":  nullableJSONString(e.FromState),
			"to_state":    e.ToState,
			"actor":       e.Actor,
			"occurred_at": e.OccurredAt,
			"detail":      nullableJSONString(e.Detail),
		}
	}

	attemptsJSON := make([]gin.H, len(attempts))
	for i, a := range attempts {
		attemptsJSON[i] = gin.H{
			"started_at":        a.StartedAt,
			"finished_at":       a.FinishedAt,
			"outcome":           nullableJSONString(a.Outcome),
			"upstream_response": nullableJSONString(a.UpstreamResponse),
			"error":             nullableJSONString(a.Error),
			"initiated_by":      a.InitiatedBy,
		}
	}

	c.JSON(http.StatusOK, gin.H{"events": eventsJSON, "attempts": attemptsJSON})
}
