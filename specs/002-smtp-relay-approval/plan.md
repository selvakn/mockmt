# Implementation Plan: SMTP Relay with Human Approval

**Branch**: `002-smtp-relay-approval` | **Date**: 2026-08-06 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/002-smtp-relay-approval/spec.md`

## Summary

Add an opt-in **relay-with-approval** mode alongside the existing capture-only mock server. When enabled, mail accepted over SMTP is stored complete and parked in an instance-wide review queue instead of being delivered; a configured reviewer inspects it in the portal and presses **Send Now**, which relays it synchronously through a real upstream provider (Gmail as the reference case). Capture-only remains the default and is left byte-for-byte unchanged.

The technical shape, from Phase 0 research:

- **Two disjoint storage paths.** Relay mode writes to four new tables; capture-only keeps writing to `emails`. Neither reads the other. This is what makes "capture-only is unchanged" (FR-002, SC-001) structurally true rather than a promise, and it delivers FR-018a for free — `queued_messages` has no `user_id` column, so relaying cannot provision a portal account for an external recipient.
- **Raw-first ingest.** `Data()` reads the whole stream to bytes and stores them; display metadata is derived by parsing a copy. The current implementation consumes the stream and keeps only subject/text/html, which cannot relay faithfully.
- **Header rewrite at the header layer.** `textproto.ReadHeader` → mutate `From`/`Reply-To`/`Sender` → `WriteHeader` → copy the body through untouched, so MIME structure and attachments survive exactly.
- **Manual SMTP conversation on the way out**, not `Client.SendMail`, because that aborts on the first bad recipient and the spec requires per-recipient outcomes.
- **The final dot is the correctness boundary.** A failure before it is confirmed; a failure awaiting its acknowledgement is *indeterminate*, and retrying one requires explicit reviewer confirmation. Without this split, FR-022's at-most-once guarantee is unenforceable.
- **At-most-once via a conditional `UPDATE`** whose affected-row count elects the winner, plus a SQLite pragma fix that this feature makes necessary.

Zero new Go dependencies: `go-smtp` ships the client, `go-sasl` the PLAIN mechanism, `go-message` the header primitives. All three are already required in `go.mod`.

## Technical Context

**Language/Version**: Go 1.25.0 (per `go.mod`); frontend is Vue 3 + Vite
**Primary Dependencies**: `github.com/emersion/go-smtp` v0.24.0 (both server and *client* — `smtp.Client`, `DialStartTLS`, `DialTLS`, `DataCommand.CloseWithResponse` all verified present), `github.com/emersion/go-sasl` (`NewPlainClient`), `github.com/emersion/go-message` v0.18.2 (`textproto.ReadHeader`/`WriteHeader` for lossless header rewriting; `mail` reader for attachment extraction), `github.com/gin-gonic/gin` v1.12.0, `github.com/mattn/go-sqlite3` v1.14.49. **No new modules.**
**Storage**: SQLite. Four new tables (`queued_messages`, `queued_recipients`, `delivery_attempts`, `audit_events`); existing `users` and `emails` untouched. Raw messages stored as BLOB, capped by `MAX_MESSAGE_BYTES` (25 MB default).
**Testing**: `go test` with stdlib `testing`, extending `internal/mockmt/smtp_test.go`. Integration tests run a throwaway TLS SMTP server on loopback as the fake upstream, with a self-signed root injected through the same `TLSConfig` field production uses — no network or real provider needed, and no plaintext bypass.
**Target Platform**: Linux server (Docker per `Dockerfile`/`docker-compose.yml`); also `go run .` locally.
**Project Type**: Web application — Go backend + Vue frontend. This feature touches both.
**Performance Goals**: Human-paced. One synchronous relay per reviewer click; an attempt bounded by `RELAY_TIMEOUT_SECONDS` (10s default) to hold SC-007. Queue listing paginated to stay responsive at SC-008's 500 messages. No throughput target — the gate is a person.
**Constraints**: No new dependencies. Capture-only behaviour must be bit-identical to today. Upstream connections must be encrypted, so plaintext is not an offered mode. Credentials must never reach a log, an API response, or the UI. Relayed messages must be byte-faithful apart from the three rewritten headers.
**Scale/Scope**: Single instance, single upstream account, a handful of reviewers, hundreds of queued messages. Backend changes are confined to `internal/mockmt`; frontend adds a review queue view.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` is still the unratified template — no project-specific principles are defined, so there are no gates to evaluate. As with feature 001, this plan instead holds itself to the conventions already evident in the codebase:

| Convention (as observed) | How this plan follows it |
|---|---|
| Env-var configuration via `getEnv` (`utils.go:9`) | All new settings are env vars with defaults; same helper |
| Fail fast on misconfiguration (`main.go:19`) | `LoadRelayConfig()` aborts startup, reporting every missing setting |
| `log`-based logging, no framework | Same; with explicit credential redaction |
| No new dependencies unless necessary | None added; the SMTP client was already vendored |
| Logic lives in `internal/mockmt`, not new modules | Same; new files in the existing package |

**Gate status: PASS (no constitution constraints defined).**

**Post-design re-check (after Phase 1)**: The design adds no dependencies, creates no new module, leaves the existing schema untouched, and confines backend changes to `internal/mockmt`. One deviation from "change nothing outside the feature" is recorded in Complexity Tracking below. **Gate status: PASS.**

## Project Structure

### Documentation (this feature)

```text
specs/002-smtp-relay-approval/
├── spec.md                      # Feature specification (with Clarifications)
├── plan.md                      # This file
├── research.md                  # Phase 0 output — 15 decisions, APIs verified against pinned deps
├── data-model.md                # Phase 1 output — 4 tables, state machine, validation rules
├── quickstart.md                # Phase 1 output — Gmail setup, config, review walkthrough
├── contracts/
│   ├── relay-api.md             # Phase 1 output — reviewer HTTP API
│   └── smtp-ingest.md           # Phase 1 output — inbound + outbound SMTP behaviour
├── checklists/
│   └── requirements.md          # Spec quality checklist (all items pass)
└── tasks.md                     # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

Existing single Go module plus the Vue frontend. No new top-level directories; no new packages.

```text
mockmt/
├── main.go                          # MODIFIED: load relay config, run crash-recovery sweep, start purge ticker
├── internal/mockmt/
│   ├── smtp.go                      # MODIFIED: raw-first Data(), branch capture vs queue on mode
│   ├── relay_config.go              # NEW: RelayConfig, LoadRelayConfig(), reviewer-list parsing
│   ├── relay_store.go               # NEW: queue persistence, conditional-claim state transitions, audit writes
│   ├── relay_message.go             # NEW: header rewrite, Bcc detection, attachment extraction
│   ├── relay_sender.go              # NEW: upstream client, per-recipient loop, outcome classification
│   ├── relay_web.go                 # NEW: /api/relay/* handlers, requireReviewer() middleware
│   ├── retention.go                 # NEW: purge sweep
│   ├── relay_limits.go              # NEW: connection cap w/ 421 greeting + whole-message I/O semaphore (R16)
│   ├── relay_limits_test.go         # NEW: over-cap gets 421; slots released on close; semaphore timeout
│   ├── database.go                  # MODIFIED: new tables in createTables(); WAL + busy_timeout DSN
│   ├── web.go                       # MODIFIED: register relay routes
│   ├── auth.go                      # unchanged — reviewer check layers on top of authMiddleware()
│   ├── utils.go                     # unchanged
│   ├── smtp_test.go                 # MODIFIED: capture-only regression coverage
│   ├── relay_sender_test.go         # NEW: fake TLS upstream — partial failure, indeterminate, dial failure
│   ├── relay_message_test.go        # NEW: header rewrite, existing Reply-To, Bcc detection
│   └── relay_store_test.go          # NEW: state machine, concurrent-claim exactly-once
├── frontend/src/
│   ├── views/ReviewQueue.vue        # NEW: queue list, state filter
│   ├── components/ReviewMessage.vue # NEW: detail, sandboxed body, attachments, Send Now / Reject
│   ├── components/Header.vue        # MODIFIED: reveal queue link when reviewer
│   ├── services/api.js              # MODIFIED: relay endpoints, authenticated blob fetch for attachments
│   └── router/index.js              # MODIFIED: /review route
├── env.example                      # MODIFIED: document the new variables
├── docker-compose.yml               # MODIFIED: mem_limit: 256m to match the memory ceiling
└── README.md                        # MODIFIED: document relay mode
```

**Structure Decision**: Keep the single Go module and the existing `internal/mockmt` package, consistent with how feature 001 and the existing OAuth/JWT code are organized (one package, one file per concern). Relay logic is split across five new files by responsibility — config, storage, message manipulation, sending, HTTP — rather than accreting into `smtp.go`, because sending and reviewing are genuinely separate concerns from ingest and each carries its own tests. No new Go package is warranted at this size.

## Complexity Tracking

> Recording one change that reaches outside the feature's own surface, since it modifies shared infrastructure.

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Change the SQLite DSN to add `_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate`, affecting all existing queries | `InitDatabase` (`database.go:37`) currently opens with no busy timeout and the default rollback journal, while a writing SMTP goroutine and a writing web goroutine already run concurrently (`main.go:27-37`). This feature turns concurrent writes from rare into routine — every approval writes a message row, recipient rows, an attempt row, and an audit row while ingest may be storing another message. Without a busy timeout those collide as `SQLITE_BUSY` errors, which would surface as spurious "failed to send" results and undermine SC-005. | *Leave the DSN alone and retry on busy in application code* — reimplements in Go what SQLite already does correctly in C, at every call site. *`SetMaxOpenConns(1)`* — serializes reads as well as writes, and makes the exactly-once guarantee depend on pool configuration rather than on the store. |

No other deviations. Everything else is additive: new files, new tables, new routes, new frontend views.

## Risk mitigations (designed upfront)

Each risk is designed out here, not left for the implementer to trip over. Full rationale in research R16–R19.

### 1. Memory — bounded to ~128 MB

Two library facts settle the design. `mattn/go-sqlite3` exposes **no incremental blob I/O**, so the whole message is in memory at `INSERT` and again at `SELECT` — which means spooling to a temp file would not help, and capping concurrency is the only lever. `go-smtp` has **no connection limit** but does expose `Server.Serve(net.Listener)`, so a limiting listener can wrap it. Separately, `go-smtp` already rejects an oversized `MAIL FROM ... SIZE=n` with `552` before transferring anything (`conn.go:360`) — early rejection is free.

| Control | Default | Purpose |
|---|---|---|
| `MAX_MESSAGE_BYTES` | 25 MB | per-message ceiling; free early rejection via `SIZE` |
| `SMTP_MAX_CONCURRENT` | 3 | inbound sessions; over-cap gets `421` and is closed |
| `RELAY_MAX_CONCURRENT_IO` | 2 | concurrent whole-message reads (send, preview, download) |
| `SMTP_READ_TIMEOUT_SECONDS` | 60 | idle timeout |
| `SMTP_WRITE_TIMEOUT_SECONDS` | 60 | idle timeout |

Worst case ≈ 25 MB × (3 + 2) = **125 MB**. `docker-compose.yml` gains `mem_limit: 256m` so the container fails locally and loudly rather than being OOM-killed by the host.

Three implementation rules that are easy to get wrong:

- **Idle timeouts ship in the same change as the cap.** `NewServer` sets no timeouts and go-smtp applies them only when non-zero (`server.go:165-168`), so today there is none. Harmless with unlimited connections; with a cap of 3, three idle sockets block all submission indefinitely. The cap without the timeouts is a regression.
- **Acquire the I/O slot before claiming the message.** Claiming first and then failing to get a slot would strand the message in `sending` — inventing the stuck state FR-028 exists to prevent. Order: acquire → claim → send → release.
- **Release the slot early on read paths.** Preview reads the raw message, extracts the one part, drops the buffer and releases the slot, *then* streams. Only the send path holds its slot for the whole attempt.

Saturation waits up to 5s, then returns `503` with `Retry-After: 5` rather than queueing indefinitely.

### 2. Attachment fetches — one helper, enforced

A single `fetchAsObjectUrl(path)` in `services/api.js` is the only path to message content: bearer-header `fetch` → `Blob` → object URL. Components never set `src` to an API path, because browsers do not attach the `Authorization` header to subresource loads and the resulting 401 looks like a broken preview rather than an auth bug.

Callers must `URL.revokeObjectURL` on unmount or message change. This is part of the mitigation, not tidiness — each object URL pins its blob for the document's lifetime, so clicking through twenty messages with large attachments would accumulate hundreds of megabytes in the tab.

### 3. TLS trust — injectable, with no footgun

`RelayConfig` carries a `TLSConfig *tls.Config`. `LoadRelayConfig` leaves it `nil` (system roots); tests inject a self-signed root generated in-process, so **tests exercise the production TLS path** instead of a plaintext bypass. `RELAY_CA_CERT_FILE` optionally appends a private CA for self-hosted upstreams.

There is deliberately **no** `RELAY_INSECURE_SKIP_VERIFY`. Such a flag gets enabled once during setup and never disabled, silently removing what FR-029 requires. The explicit CA file covers the legitimate case; the absence is recorded so it reads as a decision, not an oversight.

### 4. `Reply-To` precedence — one function, table-driven tests

All header mutation lives in one pure function, `rewriteHeaders(h *textproto.Header, relayIdentity, originalFrom string)`. `Reply-To` is set only when absent, or present but blank after trimming; a non-blank value is the sender's choice and survives.

The intuitive `h.Set("Reply-To", ...)` is wrong and fails silently — mail still sends, replies just go astray, and no smoke test catches it. Blank handling matters because `Header.Has` returns true for `Reply-To:` with an empty value. Tests cover absent, non-blank, empty, whitespace-only, and duplicate headers, paired with a golden test asserting the body is **byte-identical** after rewrite — the mechanical guarantee behind FR-008 and SC-006.
