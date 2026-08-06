---

description: "Task list for SMTP Authentication feature"
---

# Tasks: SMTP Authentication

**Input**: Design documents from `/specs/001-smtp-authentication/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/smtp-auth-protocol.md, quickstart.md

**Tests**: Included — this feature adds the repository's first Go tests (`internal/mockmt/smtp_test.go`), matching the Testing decision recorded in plan.md's Technical Context and the file already called out in its Project Structure.

**Organization**: Tasks are grouped by user story (from spec.md) to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Exact file paths are included in every task description

## Path Conventions

Existing single Go module at the repository root (see plan.md "Project Structure"). All code changes are in `internal/mockmt/` and `cmd/test_email/`; no new top-level directories.

---

## Phase 1: Setup

**Purpose**: Confirm a clean baseline before making changes

- [X] T001 Run `go build ./...` and `go test ./...` at the repository root to confirm the project currently builds and passes (0 tests exist yet, so this just confirms no build errors) before any changes are made

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core authentication infrastructure that every user story depends on — implementing `smtp.AuthSession` and credential loading. No user story can be implemented or tested until this phase is complete.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T002 Add an `authenticated bool` field to the `Session` struct in `internal/mockmt/smtp.go`; ensure `Reset()` does NOT clear it (per data-model.md state transitions — an authenticated connection may send multiple messages)
- [X] T003 Add `Username`/`Password` fields to the `Backend` struct in `internal/mockmt/smtp.go` and thread them into each `Session` created by `NewSession(c *smtp.Conn)` (depends on T002)
- [X] T004 Implement `loadSMTPCredentials() (username, password string, err error)` in `internal/mockmt/smtp.go`, reading `SMTP_USERNAME`/`SMTP_PASSWORD` via the existing `getEnv` helper (`internal/mockmt/utils.go`) and returning a descriptive error if either is empty (per research.md "Credential configuration" decision)
- [X] T005 Wire `loadSMTPCredentials()` into `StartSMTPServer()` in `internal/mockmt/smtp.go`: call it before constructing the `Backend`/starting the listener, and `log.Fatal` with a clear, actionable message (naming the missing variable) on error (depends on T004; per FR-005/FR-010)
- [X] T006 Remove the dead `AuthPlain(username, password string) error` method from `Session` in `internal/mockmt/smtp.go` (per research.md — never invoked by `go-smtp` v0.24.0, superseded by T007)
- [X] T007 Implement `AuthMechanisms() []string` (returning `[]string{sasl.Plain}`) and `Auth(mech string) (sasl.Server, error)` on `Session` in `internal/mockmt/smtp.go`, importing `github.com/emersion/go-sasl` and using `sasl.NewPlainServer(...)` with an authenticator callback that validates the presented username/password against the session's configured credentials, sets `s.authenticated = true` on success, and logs success/failure via `log.Printf` without ever logging the password (depends on T002, T003; per FR-003/FR-006 and contracts/smtp-auth-protocol.md)
- [X] T008 Run `go mod tidy` at the repository root so `github.com/emersion/go-sasl` moves from an indirect to a direct dependency in `go.mod`/`go.sum` (depends on T007's new direct import)

**Checkpoint**: Foundation ready — `AUTH PLAIN` is advertised and validated, credentials are loaded and fail-fast at startup. User story implementation can now begin.

---

## Phase 3: User Story 1 - Reject mail from unauthenticated senders (Priority: P1) 🎯 MVP

**Goal**: The SMTP server refuses to accept a mail transaction from any client that has not successfully authenticated, closing the open-relay gap.

**Independent Test**: Point a generic SMTP client at the server without providing credentials (or with incorrect ones) and attempt to send a message; the server must reject the attempt and no email is stored.

### Tests for User Story 1

- [X] T009 [US1] Unit test in `internal/mockmt/smtp_test.go`: a `Session` with `authenticated=false` calling `Mail(...)` returns `smtp.ErrAuthRequired` and no email is persisted (verify via `getEmailsByUser`/`getEmailStats` against a test DB)
- [X] T010 [US1] Unit test in `internal/mockmt/smtp_test.go`: calling `Auth("PLAIN")`'s returned `sasl.Server` with an incorrect username or password does not set `authenticated` to `true` and returns an error (per contracts/smtp-auth-protocol.md `535` case)

### Implementation for User Story 1

- [X] T011 [US1] In `internal/mockmt/smtp.go`, add the authentication gate at the top of `Mail(from string, opts *smtp.MailOptions) error`: return `smtp.ErrAuthRequired` when `!s.authenticated`, before setting `s.from` (depends on T007; makes T009 pass)
- [X] T012 [US1] Manually verify the two rejection scenarios from `quickstart.md` ("Verify rejection of unauthenticated mail" and "Verify rejection of bad credentials") against a locally running server (`go run .`); confirm SMTP response codes match `contracts/smtp-auth-protocol.md` (`502` and `535`) — no file changes, verification only

**Checkpoint**: User Story 1 is independently functional and testable — unauthenticated and badly-authenticated mail is rejected. This alone is a viable, deployable fix for the reported open-relay issue.

---

## Phase 4: User Story 2 - Send mail with valid credentials (Priority: P2)

**Goal**: Legitimate senders can still deliver mail after authentication is enforced, including multiple messages on one authenticated connection.

**Independent Test**: Configure valid SMTP credentials, authenticate a standard SMTP client (e.g. `cmd/test_email`) with them, and confirm the email is accepted and appears in the recipient's inbox exactly as before.

### Tests for User Story 2

- [X] T013 [US2] Unit test in `internal/mockmt/smtp_test.go`: a successful `Auth("PLAIN")` with correct credentials sets `authenticated=true`, and a subsequent `Mail()`/`Rcpt()`/`Data()` sequence succeeds and the email is stored (reuse existing `saveEmail`/DB helpers against a test DB)
- [X] T014 [US2] Unit test in `internal/mockmt/smtp_test.go`: after one successful mail transaction and a `Reset()` call, a second `Mail()`/`Rcpt()`/`Data()` sequence on the same already-authenticated session succeeds without calling `Auth` again

### Implementation for User Story 2

- [X] T015 [P] [US2] Update `cmd/test_email/main.go` to read/prompt for an SMTP username and password and pass `smtp.PlainAuth("", username, password, "localhost")` to `smtp.SendMail(...)` in `sendTestEmail`, per `quickstart.md`

**Checkpoint**: User Stories 1 AND 2 both work independently — authenticated senders are unaffected, unauthenticated/bad-credential senders are rejected.

---

## Phase 5: User Story 3 - Operator configures SMTP credentials (Priority: P3)

**Goal**: Operators can set SMTP credentials via environment variables, consistent with existing OAuth/JWT configuration, and get a clear failure if they forget to.

**Independent Test**: Set `SMTP_USERNAME`/`SMTP_PASSWORD`, start the server, and confirm enforcement (validated via the US1/US2 tests passing with those exact values); unset them and confirm the server fails to start with a clear error.

### Tests for User Story 3

- [X] T016 [P] [US3] Unit test in `internal/mockmt/smtp_test.go`: `loadSMTPCredentials()` returns an error when `SMTP_USERNAME` or `SMTP_PASSWORD` is unset/empty (using `t.Setenv`), and returns the configured values when both are set

### Implementation for User Story 3

- [X] T017 [P] [US3] Update `env.example` to add `SMTP_USERNAME` and `SMTP_PASSWORD` entries next to the existing `SMTP_PORT` setting, per `quickstart.md`
- [X] T018 [P] [US3] Update `README.md`: add `SMTP_USERNAME`/`SMTP_PASSWORD` to the documented `docker run` example and note in the Features/Setup sections that the SMTP server now requires authentication, per `quickstart.md`'s Docker section
- [X] T019 [US3] Manually verify the `quickstart.md` startup-failure behavior: unset `SMTP_USERNAME`/`SMTP_PASSWORD` and confirm `go run .` exits with a clear fatal error before opening the SMTP listener (depends on T005)

**Checkpoint**: All three user stories are independently functional; documentation and examples are consistent with the new requirement.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Verification and cleanup spanning all stories

- [X] T020 [P] Run `make lint` and `make vet` and fix any findings introduced by this feature
- [X] T021 [P] Run `make fmt-check` (or `make fmt`) and fix any formatting issues introduced by this feature
- [X] T022 Run `make test` (full `go test ./...`) and confirm all new and existing tests pass (depends on all Phase 2–5 implementation tasks)
- [X] T023 Execute the full `quickstart.md` walkthrough end-to-end against a locally built binary (configure credentials → authenticated send succeeds → unauthenticated send rejected → bad-credential send rejected → missing-credential startup fails) and confirm behavior matches `contracts/smtp-auth-protocol.md` exactly (depends on T022)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Setup completion — BLOCKS all user stories (T002→T003→T007 chain; T004→T005; T007→T008)
- **User Stories (Phase 3-5)**: All depend on Foundational phase completion
  - User Story 1 (P1): No dependencies on other stories
  - User Story 2 (P2): No dependencies on US1's tasks, but shares `smtp_test.go` with it — sequence to avoid conflicting edits
  - User Story 3 (P3): No dependencies on US1/US2 tasks; T019 depends on Foundational T005 only
- **Polish (Phase 6)**: Depends on all desired user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Foundational (Phase 2) — no dependencies on other stories
- **User Story 2 (P2)**: Can start after Foundational (Phase 2) — independently testable; does not require US1's gate check to exist, though both live in `Mail()`
- **User Story 3 (P3)**: Can start after Foundational (Phase 2) — independently testable; its "enforcement" claim is validated via US1/US2, per spec.md

### Within Each User Story

- Tests are written before their corresponding implementation task where practical (T009/T010 before T011; T013/T014 exercise behavior already enabled by Foundational T007, so can be written alongside T015)
- Implementation before manual verification
- Story complete before moving to the next priority (recommended, though stories are independently deliverable)

### Parallel Opportunities

- Foundational tasks T002-T008 are mostly sequential (same file, dependency chain) — no `[P]` markers in Phase 2
- Within User Story 1, T009 and T010 both edit `internal/mockmt/smtp_test.go` — do not run in parallel with each other
- Within User Story 2, T015 (`cmd/test_email/main.go`) can run in parallel with US1/US3 test-file work since it's a different file
- Within User Story 3, T016 (`smtp_test.go`), T017 (`env.example`), and T018 (`README.md`) touch three different files and can run in parallel
- In Polish, T020 and T021 (independent tooling checks) can run in parallel

---

## Parallel Example: User Story 3

```bash
# These three touch different files and have no interdependency:
Task: "Unit test loadSMTPCredentials() error/success cases in internal/mockmt/smtp_test.go"
Task: "Add SMTP_USERNAME/SMTP_PASSWORD to env.example"
Task: "Update README.md docker run example and Setup notes"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001)
2. Complete Phase 2: Foundational (T002-T008) — CRITICAL, blocks all stories
3. Complete Phase 3: User Story 1 (T009-T012)
4. **STOP and VALIDATE**: Confirm unauthenticated and bad-credential mail is rejected (independent test from spec.md)
5. This alone closes the reported open-relay security gap and can ship as the MVP

### Incremental Delivery

1. Setup + Foundational → authentication mechanism exists and is enforced-capable
2. Add User Story 1 → unauthenticated/bad-credential mail rejected → deploy (MVP, closes the security gap)
3. Add User Story 2 → confirms/updates the legitimate-sender path (`cmd/test_email`) → deploy
4. Add User Story 3 → docs/env example updated, fail-fast startup verified → deploy
5. Polish → lint/vet/fmt/full test suite/quickstart validation

### Parallel Team Strategy

With multiple developers, after Foundational (Phase 2) completes:

- Developer A: User Story 1 (T009-T012)
- Developer B: User Story 2 (T013-T015)
- Developer C: User Story 3 (T016-T019)

All three touch `internal/mockmt/smtp_test.go` for their respective test tasks — coordinate to avoid merge conflicts in that one file (e.g., land US1's test additions first, then rebase).

---

## Notes

- `[P]` tasks = different files, no dependencies
- `[Story]` label maps task to specific user story for traceability
- This feature introduces no new database entities/migrations (see data-model.md) — all "entities" are configuration/in-memory state
- No new external dependencies are introduced; `github.com/emersion/go-sasl` is already present transitively and is only promoted to a direct dependency (T008)
- Commit after each task or logical group
- Stop at any checkpoint to validate a story independently
