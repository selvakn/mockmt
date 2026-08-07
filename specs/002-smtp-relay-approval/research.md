# Phase 0 Research: SMTP Relay with Human Approval

**Feature**: `002-smtp-relay-approval` | **Date**: 2026-08-06 | **Plan**: [plan.md](./plan.md)

All API claims below were verified against the versions pinned in `go.mod`, by reading the sources in `$(go env GOMODCACHE)`. Line references are to those pinned copies.

---

## R1. Retaining the complete original message

**Decision**: In relay mode, `Session.Data(r io.Reader)` reads the entire stream into memory with `io.ReadAll` and persists those bytes verbatim as a BLOB. Display metadata (subject, bodies, attachment list) is derived by parsing a *copy* of those bytes, never by consuming the stream.

**Rationale**: FR-008 requires the relayed copy to be faithful to what the sender submitted. The current implementation (`internal/mockmt/smtp.go:71`) consumes `r` through `mail.CreateReader`, keeps only subject/plain/html, and drops attachments and all other headers on the floor — it is structurally incapable of relaying a faithful copy. Reading raw first and parsing second is the only ordering that preserves both. `go-smtp` already enforces the size ceiling while the stream is being read (`data.go:74` returns `ErrDataTooLarge`), so `io.ReadAll` is bounded by `Server.MaxMessageBytes` and cannot be used to exhaust memory.

**Alternatives considered**:
- *Parse to a structured form and re-serialize on send* — rejected. Re-encoding changes MIME boundaries, header order, and transfer encodings, breaking any DKIM signature the sender applied and violating FR-008.
- *Stream to a file on disk, store a path* — rejected for now. Adds a second storage system to back up, purge, and secure, for messages capped at 25 MB. Revisit only if the size ceiling is raised substantially.

---

## R2. Rewriting `From` without touching the body

**Decision**: Rewrite headers using `go-message/textproto`: wrap the stored bytes in a `bufio.Reader`, call `textproto.ReadHeader` (`textproto/header.go:521`), mutate the returned `Header` with `Set`/`Del`/`Add` (lines 133–197), write it back with `textproto.WriteHeader` (line 663), then `io.Copy` the *remainder of the reader* — the body — through byte-for-byte.

**Rationale**: This is precisely the operation FR-013/013a/013b describe: change three header fields, alter nothing else. `textproto` operates at the header level and never decodes or re-encodes the body, so attachments and MIME structure survive untouched. `ReadHeader` leaves the reader positioned at the first body byte, so the copy is exact.

The rewrite performed at send time:

| Header | Action | Requirement |
|--------|--------|-------------|
| `From` | `Set` to the configured relay identity | FR-013 |
| `Reply-To` | `Set` to the original submitted sender — **only if absent** | FR-013a |
| `Sender` | `Del` if present | avoids contradicting the rewritten `From` |
| everything else | untouched | FR-008 |

**Alternatives considered**:
- *`go-message.Read` + `Entity.WriteTo`* — rejected. Round-trips the whole entity tree and re-encodes parts.
- *Regex or line-scanning the raw bytes* — rejected. Header folding, continuation lines, and encoded words make this wrong in exactly the cases that matter.

---

## R3. Upstream relay client, TLS, and timeouts

**Decision**: Use the `smtp.Client` already vendored with `go-smtp` v0.24.0. Two operator-selectable TLS modes, no third:

| Mode | Constructor | Typical port |
|------|-------------|--------------|
| `starttls` (default) | `smtp.DialStartTLS` (`client.go:89`) | 587 |
| `tls` | `smtp.DialTLS` (`client.go:71`) | 465 |

Authenticate with `sasl.NewPlainClient("", user, pass)` (`go-sasl/plain.go:30`) via `Client.Auth` (`client.go:367`). Bound the attempt by setting `Client.CommandTimeout` and `Client.SubmissionTimeout` (both real fields on the struct) and by dialling through a `net.Dialer`/`tls.Dialer` with an explicit timeout, since `Dial*` do not accept one.

**Rationale**: Zero new dependencies. FR-029 forbids unencrypted transmission, so plaintext is deliberately *not* an available mode — this is how the apparent tension between FR-003 ("configure connection security") and FR-029 ("must be encrypted") is resolved: the setting selects *which* encryption, never whether. FR-020b requires a bounded attempt, and a bare `Dial` to an unreachable host can hang for the OS TCP timeout (over a minute), which would blow SC-007's 10-second budget — hence the explicit dial timeout.

**Alternatives considered**:
- *`net/smtp` from the standard library* — rejected. Frozen, no SASL abstraction, no per-phase timeouts.
- *`Client.SendMail`* — rejected, see R4.

---

## R4. Per-recipient delivery outcomes

**Decision**: Do not use `Client.SendMail`. Drive `Mail` → `Rcpt` (per address, recording each result) → `Data` → `CloseWithResponse` by hand.

**Rationale**: `SendMail` (`client.go:722`) returns on the *first* `Rcpt` error, abandoning the remaining recipients and the message entirely. FR-025 and User Story 4 scenario 3 require knowing which recipients succeeded and which did not, and requires a retry to skip those already served. Only the manual loop yields that.

The resulting semantics, which the data model encodes:

- A recipient is **rejected** if its `RCPT TO` draws a 5xx. Its own `SMTPError` is stored against that address.
- A recipient is **accepted** if `RCPT TO` succeeds *and* the final dot is acknowledged. In standard SMTP a single `250` covers the whole message — there is no per-recipient acknowledgement at DATA time (that is LMTP only, `CloseWithLMTPResponse`). So per-recipient success is decided at `RCPT`, then confirmed collectively by the final dot.
- If every `RCPT TO` is rejected, do not issue `DATA` at all; the message is a confirmed failure.

`CloseWithResponse` (`client.go:590`) returns the upstream's `250` status text, which FR-023 requires be recorded.

---

## R5. Confirmed vs indeterminate failure

**Decision**: Classify by *where* in the SMTP conversation the attempt died. The boundary is the final dot.

| Failure point | Classification | Why |
|---|---|---|
| Dial, TLS handshake, `AUTH` | `confirmed` | nothing was transmitted |
| `MAIL FROM` rejected | `confirmed` | upstream refused before content |
| all `RCPT TO` rejected | `confirmed` | no recipients, DATA never issued |
| `DATA` command rejected | `confirmed` | upstream refused before content |
| write error mid-body, before the final dot | `confirmed` | an unterminated DATA is discarded by the receiver |
| **error or timeout awaiting the final-dot reply** | **`indeterminate`** | the message was fully transmitted; the upstream may have accepted it and we simply never heard back |
| process died while state was `sending` | `indeterminate` | unknowable after the fact |

This maps onto the library cleanly: `DataCommand.close()` writes the terminating dot, and `CloseWithResponse` then reads the reply under `SubmissionTimeout` (`client.go:595-605`). A failure from the read is exactly the indeterminate case.

**Rationale**: FR-024a demands this split, and FR-022's at-most-once guarantee depends on it. Treating a post-dot timeout as a clean failure and offering one-click retry is precisely how duplicate mail gets sent. Being conservative here costs an occasional unnecessary confirmation prompt; being wrong costs a real person receiving the same mail twice.

---

## R6. At-most-once approval under concurrency

**Decision**: Claim the message with a conditional update inside a transaction, and treat the affected-row count as the lock:

```sql
UPDATE queued_messages SET state='sending', ... WHERE id=? AND state='pending_review'
```

`RowsAffected() == 0` means another reviewer won the race; that caller gets `409 Conflict` and is told the message was already handled. Only the winner opens the upstream connection. Retry from `failed` uses the same pattern with `state='failed'`.

**Additionally**: open SQLite with `_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate`. All three DSN parameters are supported by the pinned `mattn/go-sqlite3` v1.14.49 (`sqlite3.go:1128-1157`).

**Rationale**: FR-022 and SC-005. A read-then-write check has a race window; a conditional update does not, because SQLite serializes the writes. The WAL/busy-timeout change is not cosmetic — the current `InitDatabase` (`internal/mockmt/database.go:37`) opens the database with no busy timeout and the default rollback journal, while a writing SMTP goroutine and a writing web goroutine already run concurrently (`main.go:27-37`). This feature makes concurrent writes routine, so `SQLITE_BUSY` would go from rare to common. Fixing it is in scope as a prerequisite, not a nice-to-have.

**Alternatives considered**:
- *A Go `sync.Mutex`* — rejected. Guards one process, but the guarantee should live in the store where the state does.
- *`db.SetMaxOpenConns(1)`* — rejected as the primary mechanism. Serializes all reads too, and the correctness of the claim should not depend on pool configuration.

---

## R7. Crash recovery

**Decision**: On startup, before serving, sweep any row left in `sending` into `failed` with `failure_kind='indeterminate'` and a reason naming the restart. Record an audit event with a system actor.

**Rationale**: FR-028 and User Story 4 scenario 7. `sending` is only ever occupied by an in-flight attempt within a single process, so any such row found at startup is by definition orphaned. A sweep is sufficient and needs no timestamp heuristics or background reaper. Per R5 the outcome is genuinely unknown, so `indeterminate` is the honest classification.

---

## R8. Reviewer authorization

**Decision**: Parse `REVIEWER_EMAILS` (comma-separated) once at startup into a `map[string]struct{}` keyed on the lower-cased, trimmed address. A new `requireReviewer()` gin middleware runs *after* the existing `authMiddleware()` and checks the `user_email` it already sets in the context (`internal/mockmt/auth.go:227`). Reviewer status is resolved per request from config, never baked into the JWT.

**Rationale**: FR-017/017a. Deriving it per request means removing someone from the list takes effect on their next request rather than when their 24-hour token expires (`auth.go:180`) — which matters, and is what the "reviewer removed while messages are pending" edge case expects. Reusing the existing middleware keeps one authentication path.

**Alternatives considered**:
- *An `is_reviewer` claim in the JWT* — rejected. Stale for up to 24 hours after a config change.
- *A `role` column on `users`* — rejected. The spec puts reviewer management out of scope, and a column implies an editing UI that does not exist.

---

## R9. New tables rather than extending `emails`

**Decision**: Relay-mode messages go into new tables (`queued_messages`, `queued_recipients`, `delivery_attempts`, `audit_events`). The existing `emails` table and every query against it are left exactly as they are. Capture-only mode writes only to `emails`; relay mode writes only to the new tables.

**Rationale**: This is what makes FR-002 and SC-001 provable rather than merely intended. If relay mode wrote into `emails`, every existing query would need a "…and not a queued message" filter, and one missed filter would leak queued mail into an ordinary user's inbox. Separate tables make that leak structurally impossible. It also delivers FR-018a for free: `queued_messages` simply has no `user_id` column, so there is nothing for `createOrGetUser` to populate and no way for relaying to provision a portal account for an external recipient.

**Alternatives considered**:
- *Add `state`/`raw_message` columns to `emails`* — rejected. Couples the two modes in the one place the spec insists they stay independent, and makes FR-018b (previously captured mail unaffected) a migration risk.

---

## R10. Envelope recipients and blind-carbon detection

**Decision**: The envelope list is what `Session.Rcpt` accumulates (`internal/mockmt/smtp.go:60`) — already correct and already collected. Persist every envelope recipient as its own `queued_recipients` row. Separately parse the `To` and `Cc` headers, and mark a recipient `hidden=true` when its address appears in no visible header. Relay to the stored envelope list; never add a hidden address to the outgoing headers.

**Rationale**: FR-015a/015b. Address comparison normalizes case on the domain and strips display names via `mail.ParseAddressList`, so `"Ops" <A@Example.COM>` in a header matches envelope `a@example.com`. Getting this wrong in the permissive direction (marking a genuine Bcc as visible) is the dangerous failure, so the comparison treats any parse failure as "not matched", i.e. hidden — which surfaces more addresses to the reviewer rather than fewer.

---

## R11. Attachment preview and content isolation in the portal

**Decision**:

- Extract parts on demand by re-parsing the stored raw message with `go-message/mail`; do not store attachments as separate rows or files.
- Serve every part from one reviewer-only endpoint with `Content-Disposition: attachment` for downloads, `X-Content-Type-Options: nosniff`, and `Content-Security-Policy: sandbox; default-src 'none'`.
- Never set `src` on an `<img>`/`<iframe>` directly to an API URL. The portal authenticates with a bearer header (`auth.go:208`), which the browser will not attach to a subresource load. Instead `fetch()` the part with the header, turn the response into a `Blob`, and render from an object URL.
- Render the HTML body inside `<iframe sandbox srcdoc=...>` with **no** `allow-scripts` and **no** `allow-same-origin`, under a restrictive CSP.
- Preview inline: images, PDF, plain text. Everything else: download only.

**Rationale**: FR-016a–016e. Omitting `allow-scripts` stops active content executing; omitting `allow-same-origin` gives the frame an opaque origin so even if something did run it could not read the portal's session or call the API as the reviewer (FR-016c, SC-008b). `default-src 'none'` inside the frame is also what implements FR-016d — a tracking pixel or remote image has no permitted origin to load from, so reviewing a message emits no request to a sender-controlled host (SC-008c). `nosniff` plus `Content-Disposition: attachment` prevents a `.txt` containing HTML from being rendered as portal-origin content.

**Alternatives considered**:
- *Sanitize HTML server-side and render it inline* — rejected as the primary defence. Sanitizers are a denylist race; an iframe with no script permission and an opaque origin is a structural boundary. (Sanitizing *in addition* is fine, but is not what the requirement rests on.)
- *Extract attachments to rows/disk at ingest* — rejected. Duplicates bytes already held in the raw message and creates a second thing for the retention purge to miss.

---

## R12. Retention purging

**Decision**: A goroutine started at boot, ticking daily, running `UPDATE queued_messages SET raw_message=NULL, purged_at=? WHERE state IN ('sent','rejected') AND decided_at < ?`. Metadata, recipients, attempts, and audit rows are never touched. Disabled entirely when `RETENTION_DAYS` is unset or `0`. Run once at startup too, so a short-lived container still purges.

**Rationale**: FR-033–036. Nulling the blob while keeping the row is what lets FR-034 keep metadata and FR-031 keep audit records, and it makes `purged_at IS NOT NULL` the natural signal for FR-036's "identifiable as purged, not retriable". The `state IN ('sent','rejected')` predicate is FR-035 — pending and failed rows are unreachable by the statement.

---

## R13. Configuration surface and fail-fast validation

**Decision**: New environment variables, validated once at startup by a `LoadRelayConfig()` that returns an error listing *every* missing setting rather than the first.

| Variable | Default | Notes |
|---|---|---|
| `RELAY_ENABLED` | `false` | the master switch (FR-001) |
| `RELAY_HOST` | — | required when enabled |
| `RELAY_PORT` | `587` | |
| `RELAY_USERNAME` | — | required when enabled |
| `RELAY_PASSWORD` | — | required when enabled; Gmail app password |
| `RELAY_TLS_MODE` | `starttls` | `starttls` \| `tls` only — no plaintext (FR-029) |
| `RELAY_IDENTITY` | — | required when enabled; the rewritten `From` (FR-013) |
| `RELAY_TIMEOUT_SECONDS` | `10` | bounds one attempt (FR-020b) |
| `RELAY_CA_CERT_FILE` | — | optional private CA for a self-hosted upstream (R18) |
| `REVIEWER_EMAILS` | — | required non-empty when enabled (FR-017b) |
| `MAX_MESSAGE_BYTES` | `26214400` | 25 MB, matching Gmail (FR-012) |
| `SMTP_MAX_CONCURRENT` | `3` | concurrent inbound sessions; bounds memory (R16) |
| `SMTP_READ_TIMEOUT_SECONDS` | `60` | idle timeout; mandatory given the cap (R16) |
| `SMTP_WRITE_TIMEOUT_SECONDS` | `60` | idle timeout (R16) |
| `RELAY_MAX_CONCURRENT_IO` | `2` | concurrent whole-message reads (R16) |
| `RETENTION_DAYS` | `0` | `0` disables purging (FR-033) |

**Rationale**: FR-003/004/017b. Reporting all missing settings at once turns configuring a fresh deployment into one iteration instead of six. `main.go:19` already establishes fail-fast-on-misconfiguration as the house pattern, so this extends it rather than inventing anything. Defaults are chosen so that an existing deployment that upgrades and changes nothing keeps behaving exactly as before (SC-001).

---

## R14. Gmail specifics

**Findings that shape defaults and documentation**:

- `smtp.gmail.com:587` with STARTTLS, or `:465` with implicit TLS. Hence the `starttls`/587 defaults.
- Requires an **App Password**, which requires 2-Step Verification on the account. Ordinary account passwords are refused. This is the single most likely setup failure and belongs in the quickstart.
- Gmail rewrites `From` to the authenticated account unless the address is a verified "Send mail as" alias. This is exactly why FR-013 rewrites `From` ourselves — doing it deliberately means the reviewer sees in the portal what the recipient will see, instead of being surprised by Gmail's silent substitution.
- Sending limits (roughly 500 recipients/day on free accounts, 2000 on Workspace) surface as a 5xx on `RCPT`/`DATA` and land in the failure reason. Human-paced approval makes hitting these unlikely; no client-side quota tracking is planned.

---

## R15. Testing approach

**Decision**: `go test` with the standard library, extending the existing `internal/mockmt/smtp_test.go`. Three layers:

1. **Unit** — header rewrite (including the "sender already set `Reply-To`" case), Bcc detection, failure classification, config validation, reviewer-list matching.
2. **Integration against a real loopback SMTP server** — stand up a second `smtp.Server` on `127.0.0.1:0` as the fake upstream and point the relay at it. This is what makes the interesting cases testable: reject one `RCPT` and accept another for partial delivery; accept the body then hang before the final-dot reply for the indeterminate path; close the listener for dial failure.
3. **Concurrency** — fire N simultaneous approvals of one message at the claim query and assert exactly one delivery (SC-005).
4. **Mitigations** (see the addendum) — the connection cap returns `421` over the limit and releases slots on close; an idle connection is closed by the read timeout rather than holding a slot; a saturated I/O semaphore returns `503` and, critically, leaves the message in `pending_review` rather than stranding it in `sending`; `Reply-To` precedence across its five cases; and body bytes are unchanged after the header rewrite.

**Rationale**: The riskiest logic here is protocol-level and only observable end to end. A loopback server needs no new dependency, since the package under test already contains one.

**Note on the fake upstream and TLS**: R3 forbids plaintext relaying, so the test upstream needs TLS. Generate a self-signed certificate in-process at test setup and inject a `*tls.Config` with that root into the relay client — meaning the relay client must take its `tls.Config` from a struct field rather than hard-coding `nil`. This is a small testability constraint on the implementation, worth stating now because retrofitting it later is annoying. Designed out in R18.

---

# Addendum: Risk Mitigations

The four risks originally carried into implementation are designed out here rather than left for the implementer to discover. Two library facts found while doing this changed the shape of the answer.

## R16. Bounding memory (mitigates risk 1)

### What the libraries actually do

**Good news, free of charge**: `go-smtp` already advertises `SIZE <MaxMessageBytes>` in its EHLO response (`conn.go:287`) and rejects an oversized `MAIL FROM ... SIZE=n` with `552 5.3.4` *before any content is transferred* (`conn.go:360`), in addition to enforcing the ceiling during the DATA read (`data.go:63`). Setting `MaxMessageBytes` buys early rejection for well-behaved clients with no work on our part.

**Bad news, decisive**: `mattn/go-sqlite3` v1.14.49 exposes **no incremental blob I/O** — there is no binding for `sqlite3_blob_open` anywhere in the driver. Parameters are bound as whole `[]byte`. This kills the obvious mitigation of spooling large messages to a temp file: the full message must be materialized in memory at `INSERT` regardless, and again at `SELECT` when read back for relay or preview. **Capping concurrency is the only available lever.**

**Also**: `go-smtp` has no connection limit of any kind — no field, no logic. But `Server.Serve(l net.Listener)` exists (`server.go:107`), so a limiting listener can wrap it.

So the ceiling is `MAX_MESSAGE_BYTES × (concurrent inbound sessions + concurrent whole-message reads)`, and both multipliers are presently unbounded.

### Decision

Target ceiling **~128 MB**, via three mechanisms.

**1. Inbound session cap — `SMTP_MAX_CONCURRENT`, default 3.**

A small `limitedListener` wrapping `net.Listener`, passed to `Server.Serve`:

- `Accept` obtains a connection, then tries a buffered-channel semaphore without blocking.
- On success, return a wrapped `net.Conn` whose `Close` releases the slot exactly once (`sync.Once`), so accounting cannot leak. Release is tied to connection close, which the server guarantees — *not* to `Session.Logout`, which is a backend callback and a weaker guarantee.
- On failure, write `421 4.7.0 Too many concurrent connections` directly to the socket, close it, and loop to `Accept` again without ever handing it to the server.

~30 lines, no new dependency. `golang.org/x/net/netutil.LimitListener` was rejected: it blocks in `Accept`, so an over-cap client waits in the TCP backlog with no explanation and eventually times out, which is the failure mode we least want an automated sender to hit.

*Refinement on the agreed behaviour*: the rejection is delivered as the **greeting** — `421` in place of the `220` — rather than after `EHLO`. RFC 5321 §3.1 explicitly permits `421` as a greeting reply, and it saves a round trip. The outcome for the agent is identical: an immediate, standard, retryable 4xx.

**2. Idle timeouts — now mandatory, not optional.**

`NewServer` sets neither `ReadTimeout` nor `WriteTimeout`, and `go-smtp` applies them only when non-zero (`server.go:165-168`). The current server therefore has **no idle timeout at all**. That is survivable with unlimited connections; with a cap of 3 it is a trivial denial of service, since three idle sockets would block all mail submission indefinitely. `SMTP_READ_TIMEOUT_SECONDS` and `SMTP_WRITE_TIMEOUT_SECONDS` both default to 60. **The cap and the timeouts must land in the same change** — the cap alone is a regression.

**3. Whole-message read cap — `RELAY_MAX_CONCURRENT_IO`, default 2.**

A weighted semaphore guarding every path that materializes a complete raw message: relaying, attachment preview, attachment download, raw fetch.

Two rules make it behave well:

- **Acquire the slot before claiming the message.** If the claim came first and slot acquisition then failed or timed out, the message would be stranded in `sending` — inventing the exact stuck state FR-028 exists to prevent. Order is: acquire slot → claim row → send → release.
- **Release early on the read paths.** For a preview, read the raw message, extract the one requested part, drop the raw buffer and release the slot, *then* stream the part. Peak residency is the message; hold time is milliseconds. The send path cannot do this — it needs the rewritten copy for the whole SMTP conversation — so it holds its slot for the duration of the attempt.

Saturation is not queued indefinitely: acquisition waits up to 5 seconds, then returns `503` with `Retry-After: 5`. This keeps the reviewer's Send Now inside a predictable window and preserves SC-007's meaning, which is about the delivery attempt rather than about waiting in line.

### Consequences, stated plainly

At these defaults, three concurrent agent submissions are served and a fourth is told to retry; two reviewers can send or preview simultaneously and a third waits briefly. For a human-in-the-loop gate that is ample. All three values are configurable, and the ceiling formula is documented in the quickstart so an operator raising one knows what it costs.

`docker-compose.yml` currently sets no memory limit. Add `mem_limit: 256m` so the container fails loudly and locally rather than being OOM-killed by the host after a burst.

**Alternatives considered**: *Spool DATA to a temp file* — rejected; the driver cannot stream a blob into SQLite, so the peak is unchanged and only a cleanup burden is added. *Reduce `MAX_MESSAGE_BYTES` to 10 MB* — rejected; it would reject attachments that Gmail itself accepts, making the gate the reason mail fails.

---

## R17. Attachment fetches cannot use `src` (mitigates risk 2)

**Decision**: One helper in `frontend/src/services/api.js` is the *only* way message content is fetched:

```js
async function fetchAsObjectUrl(path)   // GET with bearer header -> Blob -> URL.createObjectURL
```

Components call it and must call `URL.revokeObjectURL` when the preview closes or the message changes.

**Rationale**: The portal authenticates with an `Authorization` header (`auth.go:208`), which browsers do not attach to subresource loads. An `<img src="/api/relay/...">` or `<iframe src=...>` therefore arrives unauthenticated and 401s — and it looks like a broken preview rather than an auth bug, which is what makes it a time sink. Routing every fetch through one helper means there is a single place to get it right.

The revoke step is not housekeeping. Each object URL pins its blob for the lifetime of the document, so a reviewer clicking through twenty messages with large attachments would accumulate hundreds of megabytes in the tab. Revoking on unmount is part of the mitigation, not a refinement of it.

**Alternatives considered**: *Cookie authentication for these endpoints* — rejected; introduces a second auth mechanism and CSRF surface for one use case. *Short-lived signed URLs in the query string* — rejected; puts a credential in a URL that lands in logs and browser history, for no gain over a header.

---

## R18. Injectable TLS trust (mitigates risk 3)

**Decision**: `RelayConfig` carries a `TLSConfig *tls.Config` field. `LoadRelayConfig` leaves it `nil`, meaning system roots. Tests set it directly with a self-signed root generated in-process, so **tests exercise the production TLS path** rather than a plaintext bypass.

Optionally, `RELAY_CA_CERT_FILE` appends a private CA to the pool, for operators relaying through a self-hosted upstream with an internal certificate.

**There is deliberately no `RELAY_INSECURE_SKIP_VERIFY`.** A skip-verify flag is the kind of switch that gets turned on once during setup and never turned off, silently removing the protection FR-029 exists to provide. An explicit CA file solves the same legitimate problem without the footgun, and the absence is recorded here so it reads as a decision rather than an oversight.

---

## R19. `Reply-To` precedence (mitigates risk 4)

**Decision**: All header mutation lives in one pure function, the only place in the codebase that changes headers:

```go
func rewriteHeaders(h *textproto.Header, relayIdentity, originalFrom string)
```

The `Reply-To` rule is an explicit guard: set it only when the header is absent *or* present but blank after trimming. A present, non-blank `Reply-To` is the sender's deliberate choice and is preserved (FR-013a).

**Rationale**: The intuitive implementation is `h.Set("Reply-To", originalFrom)`, which is wrong and fails silently — the mail still sends, and replies simply go to the wrong place, which no smoke test would catch. Blank-value handling is called out because `textproto.Header.Has` returns true for `Reply-To:` with an empty value, so a naive `Has` check would preserve a header that routes replies nowhere.

Covered by table-driven tests over: absent, present and non-blank, present but empty, present but whitespace-only, and multiple `Reply-To` headers. Paired with a golden test asserting the body bytes after rewrite are **byte-identical** to the input, which is the mechanical guarantee behind FR-008 and SC-006.
