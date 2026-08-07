# Contract: Relay Review API

**Feature**: `002-smtp-relay-approval` | **Date**: 2026-08-06

HTTP contract for the reviewer-facing portal API. Extends the existing gin router in `internal/mockmt/web.go`. All paths are additive; no existing endpoint changes shape.

## Common rules

**Authentication**: every endpoint below sits behind the existing `authMiddleware()`, which requires `Authorization: Bearer <jwt>` and populates `user_email` / `user_id`.

**Authorization**: every endpoint below additionally sits behind a new `requireReviewer()` middleware. A caller whose authenticated email is not in `REVIEWER_EMAILS` (case-insensitive) receives `403`. This applies to *every* endpoint in this contract with no exceptions, including message detail and attachment fetches — a non-reviewer must not be able to retrieve a queued message even when it is addressed to their own address (FR-018).

**Mode gate**: when `RELAY_ENABLED` is false, every endpoint under `/api/relay/*` returns `404`. The feature is not merely hidden in the UI; it is absent from the API (FR-002).

**Error shape**: matches the existing convention, `{"error": "message"}`.

**Standard error codes**

| Code | Meaning |
|---|---|
| 401 | missing or invalid token |
| 403 | authenticated but not a reviewer |
| 404 | relay mode disabled, or message not found |
| 409 | state conflict — another reviewer already handled it, or the message is not in a state permitting this action |
| 410 | message content was purged under the retention policy |
| 422 | action requires a confirmation that was not supplied |
| 503 | server busy — no whole-message I/O slot became free within 5s; sent with `Retry-After: 5` |

**Concurrency**: endpoints that materialize a complete raw message — send, attachment fetch, and any raw fetch — first acquire a slot from a semaphore of size `RELAY_MAX_CONCURRENT_IO` (default 2), which bounds process memory (research R16). Acquisition waits up to 5 seconds, then returns `503`. Two ordering rules matter: the slot is acquired **before** the message state is claimed, so a busy server never strands a message in `sending`; and read endpoints release the slot after extracting the requested part, before streaming it to the client.

---

## `GET /api/relay/status`

Reports the operating mode and whether the caller may act. **This one endpoint is exempt from `requireReviewer()`** — the portal needs it to decide what to render, and it discloses nothing sensitive.

Response `200`:

```json
{
  "relay_enabled": true,
  "is_reviewer": true,
  "relay_identity": "relay@example.com"
}
```

`relay_identity` is the address that will replace `From` (FR-013), shown so a reviewer knows what recipients will see. Credentials, host, and port are **never** included (FR-006). When `relay_enabled` is false the response is `{"relay_enabled": false, "is_reviewer": false}` and the portal shows no send controls (FR-005).

---

## `GET /api/relay/queue`

Lists the queue, instance-wide (FR-014).

Query parameters:

| Name | Values | Default |
|---|---|---|
| `state` | `pending_review` \| `sending` \| `sent` \| `failed` \| `rejected` \| `all` | `pending_review` |
| `limit` | 1–200 | 50 |
| `offset` | ≥ 0 | 0 |

Response `200`:

```json
{
  "total": 137,
  "messages": [
    {
      "id": 42,
      "envelope_from": "agent@myapp.local",
      "subject": "Your quote",
      "recipients": [
        {"address": "customer@example.com", "hidden": false},
        {"address": "audit@example.com",    "hidden": true}
      ],
      "recipient_count": 2,
      "state": "pending_review",
      "received_at": "2026-08-06T10:15:00Z",
      "size_bytes": 18422,
      "has_attachments": true
    }
  ]
}
```

`recipients` is the **envelope** list, and `hidden` marks addresses absent from the visible headers (FR-015a). Both list and detail views expose it, so a blind-carbon recipient is visible before the reviewer opens the message.

---

## `GET /api/relay/messages/:id`

Full detail for review (FR-016).

Response `200`:

```json
{
  "id": 42,
  "message_id": "a1b2...@localhost",
  "envelope_from": "agent@myapp.local",
  "header_from": "Sales Agent <agent@myapp.local>",
  "subject": "Your quote",
  "recipients": [
    {"address": "customer@example.com", "hidden": false, "delivered": false, "upstream_response": null}
  ],
  "text_body": "Hello,\n...",
  "html_body": "<p>Hello,</p>...",
  "attachments": [
    {"index": 0, "filename": "quote.pdf", "content_type": "application/pdf", "size_bytes": 15104, "previewable": true}
  ],
  "state": "pending_review",
  "failure_kind": null,
  "failure_reason": null,
  "received_at": "2026-08-06T10:15:00Z",
  "decided_at": null,
  "decided_by": null,
  "purged": false
}
```

`header_from` is retained and shown alongside `envelope_from` so the rewrite never hides who actually composed the message (FR-013b).

`previewable` is true for image, PDF, and plain-text types (FR-016a); everything else is download-only (FR-016b).

When the message has been purged: `purged: true`, with `text_body`, `html_body`, and `attachments` omitted. Status is still `200` — the metadata is real and the audit trail intact; only the content is gone (FR-034, FR-036).

**Response headers**: `Content-Security-Policy: default-src 'none'` and `X-Content-Type-Options: nosniff`.

The client must render `html_body` inside `<iframe sandbox srcdoc=...>` with neither `allow-scripts` nor `allow-same-origin` (FR-016c). Because the sandbox denies all origins, remote images and tracking pixels do not load, satisfying FR-016d.

---

## `GET /api/relay/messages/:id/attachments/:index`

Streams one part for preview or download (FR-016a/016b/016e).

Query parameters:

| Name | Values | Default | Effect |
|---|---|---|---|
| `disposition` | `attachment` \| `inline` | `attachment` | selects the `Content-Disposition` |

Response `200`: the raw part bytes.

**Response headers, all mandatory:**

```http
Content-Type: <declared type, or application/octet-stream>
Content-Disposition: attachment; filename="quote.pdf"
X-Content-Type-Options: nosniff
Content-Security-Policy: sandbox; default-src 'none'
Cache-Control: no-store
```

Even with `disposition=inline`, `nosniff` and the sandboxing CSP are still sent, so a part cannot be interpreted as portal-origin content (FR-016c).

`410` if the message content was purged. `404` if the index does not exist.

**Client note**: the portal authenticates with a bearer header, which a browser will not attach to an `<img src>` or `<iframe src>` subresource load. The client must `fetch()` this endpoint with the header and render from an object URL — via the single `fetchAsObjectUrl` helper in `services/api.js`, never by setting `src` to this path (research R17). Callers must `URL.revokeObjectURL` when the preview closes, or blobs accumulate for the lifetime of the document.

`503` if no I/O slot frees within 5 seconds; retry after the interval in `Retry-After`.

---

## `POST /api/relay/messages/:id/send`

Approve and relay. Synchronous — the response carries the final outcome (FR-020a).

Request body (optional):

```json
{ "confirm_duplicate_risk": true }
```

Behaviour:

1. Acquire an I/O slot (waiting up to 5s, else `503`). Then claim the message with a conditional update; if it does not win, `409` (FR-022). This order matters — claiming first and then failing to acquire would leave the message stranded in `sending`.
2. From `pending_review`, no confirmation is required.
3. From `failed` with `failure_kind='confirmed'`, no confirmation is required.
4. From `failed` with `failure_kind='indeterminate'`, `confirm_duplicate_risk: true` is **required**; without it, `422` with an error explaining a duplicate may be delivered (FR-025a).
5. Recipients already `delivered` are skipped (FR-025).
6. The attempt is bounded by `RELAY_TIMEOUT_SECONDS` (FR-020b).
7. `state: "sent"` means *every* attempted recipient was delivered. If the upstream accepts some recipients and rejects others, the response is `state: "failed"`, `failure_kind: "confirmed"` — not `"sent"` — precisely so the message stays retriable (FR-025) and the rejected recipient is not permanently unreachable. The `recipients` array in that response still reports the ones that *were* delivered as `delivered: true`, and a retry skips them per point 5.

Response `200` — delivered:

```json
{
  "state": "sent",
  "decided_by": "reviewer@example.com",
  "decided_at": "2026-08-06T10:21:03Z",
  "upstream_response": "2.0.0 OK 1754...",
  "recipients": [{"address": "customer@example.com", "delivered": true, "upstream_response": "2.1.5 OK"}]
}
```

Response `200` — failed. The request itself succeeded; the delivery did not. The reviewer needs the detail, not a bare error:

```json
{
  "state": "failed",
  "failure_kind": "indeterminate",
  "failure_reason": "Timed out waiting for the upstream server to acknowledge the message. It may or may not have been delivered.",
  "recipients": [{"address": "customer@example.com", "delivered": false, "upstream_response": null}]
}
```

`409` if already handled, `410` if purged, `422` if confirmation is required and absent.

---

## `POST /api/relay/messages/:id/reject`

Rejects a pending message (FR-026), or abandons a failed one (FR-026a).

Request body (optional): `{ "reason": "wrong recipient" }`

Permitted from `pending_review` and from `failed`. Any other state returns `409`.

Abandoning a message whose last outcome was `indeterminate` records in the audit trail that its delivery status was unknown at the time (FR-026a).

Response `200`:

```json
{ "state": "rejected", "decided_by": "reviewer@example.com", "decided_at": "2026-08-06T10:22:00Z" }
```

---

## `GET /api/relay/messages/:id/audit`

The state-change history (FR-030). Available even for a purged message, whose audit survives its content (FR-031).

Response `200`:

```json
{
  "events": [
    {"from_state": null, "to_state": "pending_review", "actor": "system", "occurred_at": "2026-08-06T10:15:00Z", "detail": "accepted from agent@myapp.local"},
    {"from_state": "pending_review", "to_state": "sending", "actor": "reviewer@example.com", "occurred_at": "2026-08-06T10:21:00Z", "detail": null},
    {"from_state": "sending", "to_state": "sent", "actor": "reviewer@example.com", "occurred_at": "2026-08-06T10:21:03Z", "detail": "250 2.0.0 OK"}
  ],
  "attempts": [
    {"started_at": "2026-08-06T10:21:00Z", "finished_at": "2026-08-06T10:21:03Z", "outcome": "sent", "initiated_by": "reviewer@example.com", "error": null}
  ]
}
```

---

## Endpoints deliberately absent

- **No message editing.** Out of scope; the reviewer approves or rejects what the sender produced.
- **No bulk approve.** Out of scope; one decision per message.
- **No reviewer management.** The reviewer list is deploy-time configuration (FR-017a).
- **No relay configuration endpoint.** Configuration is read-only from the environment, and credentials are never exposed (FR-006).
