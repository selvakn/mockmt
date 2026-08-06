# Contract: SMTP Ingest Behaviour in Relay Mode

**Feature**: `002-smtp-relay-approval` | **Date**: 2026-08-06

Defines how the inbound SMTP server behaves once relay mode is enabled, and how the outbound relay conversation is conducted. Complements [`smtp-auth-protocol.md`](../../001-smtp-authentication/contracts/smtp-auth-protocol.md) from feature 001, which still governs authentication and is unchanged.

---

## Part 1 — Inbound submission

### What does not change

Authentication behaviour is exactly as feature 001 specified. `AUTH PLAIN` against `SMTP_USERNAME`/`SMTP_PASSWORD`, `MAIL FROM` refused with `530 5.7.0 Authentication required` on an unauthenticated connection (FR-010). Relay mode does not relax or alter any of it.

In **capture-only** mode (the default), inbound behaviour is byte-for-byte what it is today: parse, `saveEmail`, done. No new limits, no new errors, nothing written to the relay tables (FR-002).

### What changes in relay mode

| Aspect | Capture-only | Relay-with-approval |
|---|---|---|
| Storage | `emails` table, owned by recipient | `queued_messages` + `queued_recipients`, no owner |
| Retained content | subject, text body, html body | complete raw message (FR-008) |
| Attachments | discarded | retained inside the raw message |
| Recipient accounts | auto-created per recipient | **never created** (FR-018a) |
| Size limit | unbounded | `MAX_MESSAGE_BYTES` (FR-012) |
| Onward delivery | none | only after human approval (FR-007) |

### Session behaviour

**`MAIL FROM`** — recorded as `envelope_from` (FR-013c). Still gated on authentication.

**`RCPT TO`** — every address is accumulated as an envelope recipient. This is the authoritative delivery list and the list the reviewer will approve (FR-015a). `Server.MaxRecipients` bounds the count.

A session with zero accepted recipients must not produce a queued message.

**`DATA`** —

1. Read the full stream into memory. `Server.MaxMessageBytes` enforces the ceiling during the read, so an oversized message fails here rather than at approval time (FR-012).
2. Parse a *copy* to derive subject and, for the reviewer's list view, whether attachments are present.
3. Parse `To` and `Cc` to compute `hidden` per envelope recipient (FR-015a, research R10).
4. Insert the message, its recipients, and the initial audit event in **one transaction**.
5. Return success only after that transaction commits (FR-009).

**Acknowledgement**: a normal `250`. The submitting client is told the message was *accepted*, not that it was delivered — an automated sender is not expected to know a human gate exists (spec assumption). The response text may note that the message was queued for review, but the code must remain `250` so ordinary clients and agent libraries behave normally.

### Connection admission and limits

Inbound sessions are capped at `SMTP_MAX_CONCURRENT` (default 3) by a limiting listener wrapping `Server.Serve` (research R16). A connection arriving when the cap is reached is answered with a `421` **greeting** in place of the usual `220`, then closed:

```text
S: 421 4.7.0 Too many concurrent connections
   (connection closed)
```

RFC 5321 §3.1 permits `421` as a greeting reply. An automated sender sees a standard retryable 4xx immediately rather than a hang.

Idle connections are bounded by `SMTP_READ_TIMEOUT_SECONDS` / `SMTP_WRITE_TIMEOUT_SECONDS` (default 60 each). These are mandatory, not tuning: `go-smtp` sets no timeouts by default, so without them three idle sockets would occupy every slot indefinitely.

Oversized mail is rejected in two places. A client declaring `MAIL FROM ... SIZE=n` with `n > MAX_MESSAGE_BYTES` is refused before transferring any content, and the ceiling is enforced again during the DATA read for clients that do not declare a size. Both are `go-smtp` behaviour, obtained by setting `MaxMessageBytes`.

### Error responses

| Condition | Response |
|---|---|
| concurrent connection cap reached | `421 4.7.0 Too many concurrent connections` (as greeting) |
| unauthenticated `MAIL FROM` | `530 5.7.0 Authentication required` |
| declared `SIZE` exceeds the limit | `552 5.3.4 Max message size exceeded` (before any transfer) |
| message exceeds `MAX_MESSAGE_BYTES` during DATA | `552 5.3.4 Message too large` (`go-smtp`'s `ErrDataTooLarge`) |
| too many recipients | `452 4.5.3 Too many recipients` |
| unparseable address | `501 5.1.3 Bad recipient address syntax` |
| storage failure | `451 4.3.0 Temporary failure` — never `250`, since FR-009 forbids acknowledging an unstored message |

---

## Part 2 — Outbound relay

Performed only when a reviewer approves, only when relay mode is enabled, and at most once per message (FR-022).

### Connection

| Mode | Constructor | Typical port |
|---|---|---|
| `starttls` (default) | `smtp.DialStartTLS` | 587 |
| `tls` | `smtp.DialTLS` | 465 |

Plaintext is not an available mode (FR-029). The dial carries an explicit timeout, and `Client.CommandTimeout` / `Client.SubmissionTimeout` bound the rest, so the whole attempt fits inside `RELAY_TIMEOUT_SECONDS` (FR-020b).

Certificate trust comes from `RelayConfig.TLSConfig`: `nil` means system roots, `RELAY_CA_CERT_FILE` appends a private CA, and tests inject a self-signed root so they exercise this same path. There is no option to skip verification (research R18).

Before opening the connection, the sender acquires a slot from the `RELAY_MAX_CONCURRENT_IO` semaphore, and it acquires that slot **before** claiming the message. Claiming first and then failing to acquire would strand the message in `sending` (research R16).

Authentication is `AUTH PLAIN` via `sasl.NewPlainClient("", RELAY_USERNAME, RELAY_PASSWORD)`. Credentials never appear in logs or errors (FR-006, FR-032).

### Header rewrite, applied to a copy in flight

The stored `raw_message` is never mutated; the rewrite happens on the way out (research R2).

| Header | Action |
|---|---|
| `From` | set to `RELAY_IDENTITY` (FR-013) |
| `Reply-To` | set to the original submitted sender, **only if the message does not already have one** (FR-013a) |
| `Sender` | removed if present |
| everything else | untouched, byte-for-byte (FR-008) |

Hidden (blind-carbon) recipients are **not** added to any header (FR-015b).

### Conversation

```text
MAIL FROM:<RELAY_IDENTITY>          ← envelope sender is the relay account (FR-013c)
RCPT TO:<each undelivered recipient>  ← record each result individually
DATA                                 ← only if ≥1 RCPT was accepted
<rewritten headers + original body>
.                                    ← the final dot; the classification boundary
```

Recipients already marked `delivered` from a previous attempt are omitted (FR-025). If every `RCPT TO` is rejected, `DATA` is not issued and the attempt is a confirmed failure.

Per-recipient success is decided at `RCPT TO`. Standard SMTP returns one `250` for the whole message, not one per recipient, so acceptance is recorded per address at `RCPT` and confirmed collectively by the final dot (research R4).

### Outcome classification

The boundary is the final dot. Everything before it is knowable; the acknowledgement is not.

| Failure point | Outcome |
|---|---|
| dial, TLS handshake, `AUTH` | `confirmed_failed` |
| `MAIL FROM` rejected | `confirmed_failed` |
| all `RCPT TO` rejected | `confirmed_failed` |
| `DATA` command rejected | `confirmed_failed` |
| write error mid-body, before the final dot | `confirmed_failed` |
| **error or timeout awaiting the final-dot reply** | **`indeterminate`** |
| final dot acknowledged | `sent`, storing the `250` text (FR-023) |
| process died while `sending` | `indeterminate`, set by the startup sweep (FR-028) |

An `indeterminate` outcome means the message was fully transmitted and the upstream may have accepted it. Retrying one therefore requires explicit reviewer confirmation, because at-most-once cannot be guaranteed for it (FR-024a, FR-025a).

### Logging

Each attempt is logged with its outcome. Never logged: credentials in any form, or full message bodies (FR-032). Recipient addresses and upstream status text are logged, as they are needed to diagnose delivery problems.
