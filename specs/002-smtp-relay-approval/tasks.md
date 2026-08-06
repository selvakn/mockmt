---

description: "Task list for SMTP Relay with Human Approval"
---

# Tasks: SMTP Relay with Human Approval

**Input**: Design documents from `/specs/002-smtp-relay-approval/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included. Not optional here — research R15 defines the testing strategy, plan.md lists the test files in the source tree, and several success criteria (SC-005 exactly-once, SC-006 byte-faithful relay, SC-008b content isolation, SC-010 no account leakage) are only verifiable by test. The riskiest logic in this feature is protocol-level and observable only end to end.

**Organization**: Tasks are grouped by user story so each can be implemented, tested, and demoed independently.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete work)
- **[Story]**: US1–US5, mapping to the user stories in spec.md
- Every task names an exact file path

## Path Conventions

Web application: Go backend in `internal/mockmt/`, Vue frontend in `frontend/src/`. Go tests live beside the code they test, per the existing `internal/mockmt/smtp_test.go`.

## Story ordering note

US1 and US2 are both P1. **US2 ships first** — it is the safety boundary, and the spec states Story 1 cannot ship without it. An instance that could accidentally deliver real mail is worse than no feature at all, so the off-by-default gate is proven before anything can be relayed.

The memory and concurrency guards (research R16) sit in Foundational rather than Polish, because raw-message storage arrives with US1 and the caps must exist before it does.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Groundwork that everything else assumes

- [X] T001 Change the SQLite DSN in `InitDatabase` to append `_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate` in `internal/mockmt/database.go` (plan Complexity Tracking: concurrent SMTP + web writes become routine in this feature and would otherwise collide as `SQLITE_BUSY`)
- [X] T002 [P] Add the relay, limit, and retention variables to `env.example` with the defaults from research R13
- [X] T003 [P] Add `mem_limit: 256m` to the `mockmt` service in `docker-compose.yml` to match the ~125 MB ceiling
- [X] T004 [P] Create the test harness in `internal/mockmt/relay_testsupport_test.go`: an in-process self-signed certificate generator, and `startFakeUpstream(opts)` returning a listening address plus a `*tls.Config` trusting it, with options to reject a named recipient, to accept the body then never reply, and to refuse connections

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Configuration, schema, access control, and the resource guards that every story depends on

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T005 Create `internal/mockmt/relay_config.go` with the `RelayConfig` struct and `LoadRelayConfig()` reading every variable from research R13, returning an error that lists **all** missing settings rather than the first
- [X] T006 Add reviewer-list parsing and `IsReviewer(email string) bool` to `internal/mockmt/relay_config.go`, matching case-insensitively on the trimmed address, resolved per request rather than cached in a token
- [X] T007 Add the `TLSConfig *tls.Config` field and optional `RELAY_CA_CERT_FILE` loading to `internal/mockmt/relay_config.go`; leave it `nil` for system roots and provide **no** skip-verify option (research R18)
- [X] T008 [P] Write config tests in `internal/mockmt/relay_config_test.go` covering aggregate error reporting, defaults, reviewer matching, and CA file loading
- [X] T009 Add the four tables from data-model.md to `createTables()` in `internal/mockmt/database.go` — `queued_messages`, `queued_recipients`, `delivery_attempts`, `audit_events` — with their indexes and `CHECK` constraints, leaving `users` and `emails` untouched
- [X] T010 Create `internal/mockmt/relay_limits.go` with a limiting `net.Listener` capped at `SMTP_MAX_CONCURRENT`, writing `421 4.7.0 Too many concurrent connections` as the greeting and closing when over cap, releasing its slot from the wrapped conn's `Close` via `sync.Once`
- [X] T011 Add the whole-message I/O semaphore sized `RELAY_MAX_CONCURRENT_IO` to `internal/mockmt/relay_limits.go`, with acquisition bounded at 5 seconds
- [X] T012 [P] Write guard tests in `internal/mockmt/relay_limits_test.go`: the connection over cap receives `421`, slots are released on close, an idle connection is closed by the read timeout, and semaphore acquisition times out rather than blocking forever
- [X] T013 Wire the server limits in `StartSMTPServer` in `internal/mockmt/smtp.go`: set `MaxMessageBytes`, `MaxRecipients`, `ReadTimeout`, and `WriteTimeout`, then serve through the limiting listener with `Server.Serve`. **The timeouts must land with the cap** — `NewServer` sets none, so three idle sockets would otherwise block all submission
- [X] T014 Create `internal/mockmt/relay_store.go` with a transaction helper and `appendAuditEvent(tx, messageID, from, to, actor, detail)`, written in the same transaction as the state change it records
- [X] T015 Create `internal/mockmt/relay_web.go` with the `requireReviewer()` gin middleware, layered after the existing `authMiddleware()` and reading the `user_email` it already sets
- [X] T016 Register the `/api/relay` route group behind `authMiddleware()` + `requireReviewer()` in `internal/mockmt/web.go`, returning `404` for every path under it when relay mode is disabled (note: only `/queue` is registered so far, as a placeholder proving the gate; the rest register in US1/US3/US4/US5 as their handlers land)
- [X] T017 Load the relay config at startup in `main.go`, aborting on error before the database or servers start, matching the existing `LoadSMTPCredentials` fail-fast pattern (T023 status endpoint and T024 startup logging pulled forward alongside this, since they're the same startup sequence)

**Checkpoint**: Configuration, schema, access control, and resource guards exist. User story work can begin.

---

## Phase 3: User Story 2 - Operator turns relay capability on and off (Priority: P1) 🔒 Safety gate

**Goal**: Relay is off unless deliberately switched on, misconfiguration is fatal at startup, and an operator can always tell which mode an instance is in.

**Independent Test**: Deploy with no relay configuration — confirm the portal shows no send controls, `/api/relay/*` returns `404`, and nothing can leave the system. Then enable relay with valid settings and confirm the queue and send controls appear. Then enable it with an incomplete config and confirm the process refuses to start.

### Tests for User Story 2

- [X] T018 [P] [US2] Test that startup fails when relay is enabled with incomplete upstream settings, and that the error names every missing variable, in `internal/mockmt/relay_config_test.go` (written under T008 as `TestLoadRelayConfigReportsAllMissingSettings`)
- [X] T019 [P] [US2] Test that startup fails when relay is enabled with an empty reviewer list (FR-017b), in `internal/mockmt/relay_config_test.go` (written under T008 as `TestLoadRelayConfigFailsOnEmptyReviewerList`)
- [X] T020 [P] [US2] Test that every `/api/relay/*` path returns `404` when relay mode is disabled, in `internal/mockmt/relay_web_test.go`
- [X] T021 [P] [US2] Capture-only regression (SC-001): with relay disabled, ingest still writes to `emails`, creates the recipient user, and writes nothing to the relay tables, in `internal/mockmt/smtp_test.go`
- [X] T022 [P] [US2] Test that no credential value appears in the startup log, the status response, or any error string (SC-009), in `internal/mockmt/relay_config_test.go`

### Implementation for User Story 2

- [X] T023 [US2] Implement `GET /api/relay/status` in `internal/mockmt/relay_web.go` per contracts/relay-api.md, returning mode, reviewer status, and relay identity but never host, port, or credentials; exempt this one endpoint from `requireReviewer()` (done in Foundational alongside T017, same startup sequence)
- [X] T024 [US2] Log the active mode, upstream host/port, identity, reviewer count, and retention setting at startup in `main.go`, with credentials omitted (done in Foundational alongside T017, same startup sequence)
- [X] T025 [P] [US2] Add `getRelayStatus()` to `frontend/src/services/api.js`
- [X] T026 [US2] Show the relay-mode indicator and reveal the review queue link only when `relay_enabled && is_reviewer` in `frontend/src/components/Header.vue`

**Checkpoint**: The safety boundary is provable. An unconfigured instance cannot deliver mail, and a misconfigured one will not start.

---

## Phase 4: User Story 1 - Reviewer releases a queued message (Priority: P1) 🎯 MVP

**Goal**: Mail submitted by an agent is held, reviewed in full including attachments, and delivered to real recipients only on an explicit Send Now.

**Independent Test**: Enable relay mode, submit a message with an attachment and a Bcc from any mail client, confirm it appears in the queue and is not delivered, press Send Now, and confirm the real recipient receives it with content intact while the portal shows it sent with the approver recorded.

### Tests for User Story 1

- [X] T027 [P] [US1] Table-driven `rewriteHeaders` tests in `internal/mockmt/relay_message_test.go` covering `Reply-To` absent, present and non-blank, present but empty, present but whitespace-only, and duplicated — the non-blank value must survive (FR-013a)
- [X] T028 [P] [US1] Golden test in `internal/mockmt/relay_message_test.go` asserting the body bytes after a header rewrite are byte-identical to the input, including a multipart message with an attachment (FR-008, SC-006)
- [X] T029 [P] [US1] Tests for envelope-versus-header recipients in `internal/mockmt/relay_message_test.go`: a Bcc address is marked hidden, a `To` address is not, matching is case-insensitive and strips display names, and an unparseable header resolves to hidden
- [X] T030 [P] [US1] Store test in `internal/mockmt/relay_store_test.go` that enqueueing writes the message, every envelope recipient, and the initial audit event in one transaction, and that a failed insert leaves nothing behind
- [X] T031 [P] [US1] Concurrency test in `internal/mockmt/relay_store_test.go`: N goroutines claim the same message simultaneously, exactly one wins, and the losers are told it was already handled (FR-022, SC-005) — verified green under `-race`, 5 repeated runs
- [X] T032 [P] [US1] Sender happy-path test in `internal/mockmt/relay_sender_test.go` against the fake TLS upstream, asserting the delivered message has `From` rewritten to the relay identity, `Reply-To` set to the original sender, and the body unchanged
- [X] T033 [P] [US1] API tests in `internal/mockmt/relay_web_test.go` for queue listing with state filter and pagination, message detail, and a successful send
- [X] T034 [P] [US1] Authorization test in `internal/mockmt/relay_web_test.go`: an authenticated non-reviewer receives `403` from every relay endpoint, **including for a message addressed to their own address** (FR-018)
- [X] T035 [P] [US1] Test in `internal/mockmt/relay_store_test.go` that queueing and relaying to external recipients creates zero rows in `users` (FR-018a, SC-010)

### Implementation for User Story 1

- [X] T036 [US1] Create `internal/mockmt/relay_message.go` with metadata parsing from raw bytes — subject, text body, HTML body, and the attachment list with filename, content type, and size — operating on a copy so the raw bytes are never consumed
- [X] T037 [US1] Add hidden-recipient detection to `internal/mockmt/relay_message.go`, comparing envelope addresses against those parsed from `To` and `Cc` and resolving any ambiguity to hidden (research R10)
- [X] T038 [US1] Add the pure `rewriteHeaders(h *textproto.Header, relayIdentity, originalFrom string)` to `internal/mockmt/relay_message.go` using `textproto.ReadHeader`/`WriteHeader`, setting `From`, conditionally setting `Reply-To`, deleting `Sender`, and copying the body through untouched
- [X] T039 [US1] Add `extractPart(raw []byte, index int)` to `internal/mockmt/relay_message.go` returning one attachment's bytes, content type, and filename
- [X] T040 [US1] Add `insertQueuedMessage` to `internal/mockmt/relay_store.go`, writing the message, its recipients, and the initial audit event in a single transaction
- [X] T041 [US1] Add `listQueue(state, limit, offset)` with total count to `internal/mockmt/relay_store.go`
- [X] T042 [US1] Add `getQueuedMessage(id)` returning the message with its recipients to `internal/mockmt/relay_store.go`
- [X] T043 [US1] Add `claimForSend(id, reviewer)` to `internal/mockmt/relay_store.go` as a conditional `UPDATE ... WHERE id=? AND state='pending_review'` whose `RowsAffected` elects the winner, plus `markSent` recording timestamp, approver, and upstream response (implemented as `tryClaimMessage` with a `fromStates` list, so US4's retry reuses the same primitive instead of duplicating it)
- [X] T044 [US1] Create `internal/mockmt/relay_sender.go` with connection setup: `DialStartTLS` or `DialTLS` by mode, an explicit dial timeout, `CommandTimeout`/`SubmissionTimeout`, the injected `TLSConfig`, and `sasl.NewPlainClient` authentication (note: implemented via raw-conn dial + `NewClientStartTLS`/`tls.Client`+`NewClient`, since `DialStartTLS`/`DialTLS` accept no dial timeout and would otherwise run STARTTLS setup under the library's un-overridable 5-minute default; a deadline is set directly on the raw connection instead)
- [X] T045 [US1] Add the manual send loop to `internal/mockmt/relay_sender.go`: `Mail` with the relay identity as envelope sender, `Rcpt` per undelivered recipient recording each result, `Data`, the rewritten message, then `CloseWithResponse` — never `Client.SendMail`, which aborts on the first bad recipient (failure classification per research R5 built in now rather than deferred to T064, since a send path with no failure branch would strand messages in `sending`)
- [X] T046 [US1] Rewrite `Session.Data` in `internal/mockmt/smtp.go` to read the full stream to bytes first, then branch: capture-only keeps today's `saveEmail` path exactly, relay mode stores raw and queues. Acknowledge only after the transaction commits (FR-009)
- [X] T047 [US1] Implement `GET /api/relay/queue` and `GET /api/relay/messages/:id` in `internal/mockmt/relay_web.go` per contracts/relay-api.md, exposing the envelope recipient list with the `hidden` flag and both `envelope_from` and `header_from` (note: `has_attachments` in the list view is computed by a lightweight header-only scan, `messageHasAttachments`, rather than the full `parseMessageMetadata`, to avoid decoding every part body just to answer a boolean for up to 200 rows)
- [X] T048 [US1] Implement `GET /api/relay/messages/:id/attachments/:index` in `internal/mockmt/relay_web.go` with `Content-Disposition`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox; default-src 'none'`, and `Cache-Control: no-store`; **release the I/O slot after extracting the part, before streaming it**
- [X] T049 [US1] Implement `POST /api/relay/messages/:id/send` in `internal/mockmt/relay_web.go`, ordered **acquire I/O slot → claim message → send → release**, returning `503` with `Retry-After` if no slot frees in 5s and `409` if the claim is lost. A failed delivery returns `200` with the failure detail, not an error status (basic `markFailed` + `delivery_attempts` recording included now for the reason above; US4 extends retry/abandon on top of the same primitives)
- [X] T050 [P] [US1] Add the relay endpoints and the single `fetchAsObjectUrl(path)` helper to `frontend/src/services/api.js` — bearer-header `fetch` → `Blob` → object URL — and export nothing that would let a component set `src` to an attachment path
- [X] T051 [US1] Create `frontend/src/views/ReviewQueue.vue` listing sender, recipients with hidden markers, subject, received time, and state, with a state filter
- [X] T052 [US1] Create `frontend/src/components/ReviewMessage.vue` rendering the HTML body in `<iframe sandbox srcdoc>` with neither `allow-scripts` nor `allow-same-origin`, previewing images/PDF/text inline via `fetchAsObjectUrl`, offering download otherwise, and calling `URL.revokeObjectURL` on unmount and on message change (Send Now wired here; Reject deferred to US3/T057 as scoped)
- [X] T053 [US1] Add the `/review` route in `frontend/src/router/index.js`

**Checkpoint**: MVP. An agent's mail is gated behind a human, reviewed in full, and delivered only on approval.

---

## Phase 5: User Story 3 - Reviewer rejects a message (Priority: P2)

**Goal**: The gate can say no, and the refusal is recorded.

**Independent Test**: Queue a message, reject it, confirm no delivery occurs, the state reads Rejected with the deciding person and time, and Send Now is gone.

### Tests for User Story 3

- [X] T054 [P] [US3] Test in `internal/mockmt/relay_web_test.go` that rejecting moves the message to `rejected`, attempts no delivery, records the decider, and that a second reject or a send afterwards returns `409`

### Implementation for User Story 3

- [X] T055 [US3] Add `rejectMessage(id, reviewer, reason)` to `internal/mockmt/relay_store.go` as a conditional update from `pending_review`, writing the audit event in the same transaction (implemented with a `fromStates` list like `tryClaimMessage`, so T067's abandon-from-failed reuses it rather than duplicating)
- [X] T056 [US3] Implement `POST /api/relay/messages/:id/reject` in `internal/mockmt/relay_web.go`
- [X] T057 [US3] Add the Reject action with an optional reason to `frontend/src/components/ReviewMessage.vue`, and hide both actions once the message is terminal

**Checkpoint**: Approve and reject both work; every decision is attributed.

---

## Phase 6: User Story 4 - Delivery to the upstream provider fails (Priority: P2)

**Goal**: Failures are honest, classified, and recoverable — and a possibly-delivered message is never quietly retried into a duplicate.

**Independent Test**: Point relay at a rejecting upstream, approve a message, confirm Failed with a readable reason and a retry that works once fixed. Separately, make the upstream accept the body then never reply, and confirm the message settles as Failed–indeterminate and cannot be retried without confirmation.

### Tests for User Story 4

- [X] T058 [P] [US4] Partial-failure test in `internal/mockmt/relay_sender_test.go`: the fake upstream rejects one `RCPT` and accepts another; the accepted recipient is marked delivered and the rejected one is not
- [X] T059 [P] [US4] Indeterminate test in `internal/mockmt/relay_sender_test.go`: the upstream accepts the body then never replies to the final dot; the outcome is `indeterminate`, not `confirmed_failed`
- [X] T060 [P] [US4] Dial-failure test in `internal/mockmt/relay_sender_test.go`: an unreachable upstream yields `confirmed_failed` within the configured timeout
- [X] T061 [P] [US4] Crash-recovery test in `internal/mockmt/relay_store_test.go`: a row left in `sending` is settled as Failed–indeterminate by the startup sweep, with a `system` audit event
- [X] T062 [P] [US4] Retry test in `internal/mockmt/relay_sender_test.go`: a retry omits recipients already marked delivered (FR-025) (satisfied by `TestRelaySendSkipsAlreadyDeliveredRecipients`, written under T032 since the sender's classification was built complete from the start)
- [X] T063 [P] [US4] Test in `internal/mockmt/relay_web_test.go` that retrying an indeterminate message without `confirm_duplicate_risk` returns `422`, and succeeds with it (FR-025a)

### Implementation for User Story 4

- [X] T064 [US4] Add failure classification to `internal/mockmt/relay_sender.go` per the table in contracts/smtp-ingest.md: everything up to and including a write error before the final dot is `confirmed`; a failure awaiting the final-dot reply is `indeterminate` (done as part of T044/T045 in Phase 4 — a send path with no failure branch would have stranded messages in `sending`)
- [X] T065 [US4] Add `markFailed(id, kind, reason)` and per-recipient outcome recording to `internal/mockmt/relay_store.go`, plus a `delivery_attempts` row per try (done as part of T049 in Phase 4, for the same reason)
- [X] T066 [US4] Extend `POST /api/relay/messages/:id/send` in `internal/mockmt/relay_web.go` to accept retries from `failed`, requiring `confirm_duplicate_risk` when the prior outcome was indeterminate (implemented as `tryClaimMessageForSend`, re-checking the confirmation guard atomically inside the same conditional-claim transaction — a separate peek-then-claim would race against a concurrent reviewer's indeterminate failure)
- [X] T067 [US4] Add abandon — the `failed → rejected` transition — to `internal/mockmt/relay_store.go` and its endpoint in `internal/mockmt/relay_web.go`, recording in the audit that delivery status was unknown when the prior outcome was indeterminate (FR-026a) (done alongside T056 in Phase 5, since `rejectMessage`'s `fromStates` list naturally covers both `pending_review` and `failed`)
- [X] T068 [US4] Add the startup sweep to `internal/mockmt/relay_store.go` and call it from `main.go` before serving, settling every `sending` row as Failed–indeterminate (FR-028)
- [X] T069 [US4] Surface failure kind, reason, and per-recipient outcomes in `frontend/src/components/ReviewMessage.vue`, with retry gated behind an explicit duplicate-risk checkbox for indeterminate messages, plus an Abandon action

**Checkpoint**: Every delivery outcome is attributable, recoverable, and honest about what it does not know.

---

## Phase 7: User Story 5 - Auditing what was released and by whom (Priority: P3)

**Goal**: The system can answer "who let that email out" long after the fact.

**Independent Test**: Approve one message and reject another, then inspect both histories and confirm actor, timestamp, and outcome for every transition — including after content has been purged.

### Tests for User Story 5

- [X] T070 [P] [US5] Test in `internal/mockmt/relay_store_test.go` that every transition produces an audit event with actor and timestamp, and that the trail survives content purging (FR-031) (purge simulated directly via SQL, since retention purging itself is a Phase 8 task)

### Implementation for User Story 5

- [X] T071 [US5] Add `getAuditTrail(id)` returning events and delivery attempts to `internal/mockmt/relay_store.go`
- [X] T072 [US5] Implement `GET /api/relay/messages/:id/audit` in `internal/mockmt/relay_web.go`, available for purged messages
- [X] T073 [US5] Add the history panel to `frontend/src/components/ReviewMessage.vue`

**Checkpoint**: All five stories independently functional.

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Retention, security verification, and documentation. Retention belongs to no single story — it came from clarification Q5 and applies across all of them.

- [X] T074 [P] Create `internal/mockmt/retention.go` purging `raw_message` and setting `purged_at` for terminal messages older than `RETENTION_DAYS`, never touching metadata, recipients, attempts, or audit rows
- [X] T075 [P] Retention tests in `internal/mockmt/retention_test.go`: terminal messages are purged, pending and failed messages never are regardless of age (FR-035), audit records survive (SC-009a/009b), and a purged message is not retriable
- [X] T076 Start the daily purge ticker and run one sweep at startup from `main.go`, skipping entirely when `RETENTION_DAYS` is `0`
- [X] T077 Handle purged messages in `internal/mockmt/relay_web.go` and `frontend/src/components/ReviewMessage.vue`: detail reports `purged: true`, attachment fetches return `410`, and the UI says the content was purged rather than showing a blank message (FR-036) (already satisfied by T047/T048/T052; verified explicitly with a dedicated test rather than assumed)
- [X] T078 [P] Security test in `internal/mockmt/relay_web_test.go` with a hostile sample message asserting the response headers that prevent active content executing in the portal session (SC-008b)
- [X] T079 [P] Security test in `internal/mockmt/relay_web_test.go` asserting no remote-content fetch is triggered by review, so a tracking pixel cannot report the message as read (SC-008c)
- [X] T080 [P] Document relay mode in `README.md`: the two modes, the configuration surface, the memory ceiling formula, and the Gmail App Password requirement
- [X] T081 [P] Final pass over `env.example` confirming every variable in research R13 is present with its documented default (already complete from T002)
- [X] T082 Walk through `quickstart.md` end to end against a real Gmail account, including a message with an attachment and a Bcc, and correct any drift (a live Gmail run was not possible in this environment — no outbound network or real credentials; instead every claim was cross-checked line-by-line against actual code behavior. Found and fixed two real drifts — the indeterminate failure wording and the SMTP startup log line — plus softened one overstated UI claim in step 8. A live run against a real Gmail account is still recommended before shipping.)
- [X] T083 Run `make ci` (fmt-check, vet, lint, test) and fix everything it reports (0 lint issues, all tests pass; had to redirect `GOCACHE`/`GOLANGCI_LINT_CACHE` to scratch directories due to a sandboxed permission restriction on the default cache paths, unrelated to the code itself)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies
- **Foundational (Phase 2)**: depends on Setup — **blocks every user story**
- **US2 (Phase 3)**: depends on Foundational. Ships first among the P1s as the safety gate
- **US1 (Phase 4)**: depends on Foundational. Should follow US2 so that nothing can relay before the off-by-default gate is proven
- **US3 (Phase 5)**: depends on Foundational; shares the store and detail view with US1
- **US4 (Phase 6)**: depends on US1's sender existing, since it classifies and recovers that sender's failures
- **US5 (Phase 7)**: depends on Foundational only — audit events are written from Foundational onward, so the history view can be built any time after
- **Polish (Phase 8)**: depends on the stories being delivered

### User Story Dependencies

- **US2 (P1)**: independent. Testable with no queue at all
- **US1 (P1)**: independent of US3–US5. Sequenced after US2 for safety, not for technical need
- **US3 (P2)**: independent; only needs the store and an endpoint
- **US4 (P2)**: **the one genuine cross-story dependency** — it classifies and recovers failures from the sender that US1 builds
- **US5 (P3)**: independent; reads audit rows that earlier phases already write

### Within Each User Story

- Tests before implementation; confirm they fail first
- Message parsing and store before sender; sender before endpoints; endpoints before UI

### Parallel Opportunities

- T002, T003, T004 in Setup
- T008 and T012 in Foundational, alongside T009's schema work
- All of T018–T022 (US2 tests), all of T027–T035 (US1 tests), all of T058–T063 (US4 tests)
- T036–T039 all touch `relay_message.go` and must be sequential; T040–T043 likewise for `relay_store.go`
- Backend and frontend within a story split cleanly: T050 runs alongside T047–T049
- With a second developer, US3 and US5 can proceed in parallel with US4

---

## Parallel Example: User Story 1 tests

```bash
# All nine US1 tests are independent and touch four different files:
Task: "rewriteHeaders Reply-To table tests in internal/mockmt/relay_message_test.go"     # T027
Task: "Golden byte-identical body test in internal/mockmt/relay_message_test.go"          # T028
Task: "Envelope vs header Bcc detection in internal/mockmt/relay_message_test.go"         # T029
Task: "Enqueue transaction test in internal/mockmt/relay_store_test.go"                   # T030
Task: "Concurrent claim exactly-once test in internal/mockmt/relay_store_test.go"         # T031
Task: "Sender happy path vs fake TLS upstream in internal/mockmt/relay_sender_test.go"    # T032
Task: "Queue/detail/send API tests in internal/mockmt/relay_web_test.go"                  # T033
Task: "Non-reviewer 403 on all endpoints in internal/mockmt/relay_web_test.go"            # T034
Task: "Zero user rows created by relaying in internal/mockmt/relay_store_test.go"         # T035
```

---

## Implementation Strategy

### MVP scope

Phases 1, 2, 3, and 4 — Setup, Foundational, US2, US1. That delivers the whole point of the feature: an agent's mail is held, a human reviews it in full, and it goes out only when they say so, on an instance that cannot relay unless deliberately configured to.

US3 (reject) is only four tasks and completes the reviewer's vocabulary, so it is a natural immediate follow-on.

### Incremental delivery

1. Setup + Foundational → guards, schema, and config in place
2. **US2** → prove the instance cannot deliver mail unless told to
3. **US1** → the gate works end to end. **Stop and validate.** Demo
4. US3 → the gate can say no
5. US4 → failures are honest and recoverable
6. US5 → history is queryable
7. Polish → retention bounds disk, security tests confirm isolation

### Sequencing risks worth respecting

- **T013's timeouts must not be deferred.** The connection cap without idle timeouts is strictly worse than no cap — three idle sockets would block all submission. They are one change.
- **T049's ordering is load-bearing.** Acquire the I/O slot before claiming the message. Reversing it strands messages in `sending` under load, which is exactly the stuck state FR-028 exists to prevent.
- **T046 changes existing behaviour.** It is the one task that touches the capture-only path. T021 (the SC-001 regression test) should be green before and after it.

---

## Notes

- 83 tasks: 4 setup, 13 foundational, 9 for US2, 27 for US1, 4 for US3, 12 for US4, 4 for US5, 10 polish
- 28 of the 83 tasks write test files, and 35 are marked [P]; the test ratio is deliberate, since protocol-level behaviour and the safety guarantees are only observable end to end
- Every task names an exact file path; tasks sharing a file are not marked [P]
- Commit after each task or logical group
- Stop at any checkpoint to validate a story independently
