---

description: "Task list for Local End-to-End Test Environment"
---

# Tasks: Local End-to-End Test Environment

**Input**: Design documents from `/specs/003-e2e-docker-testrig/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included, but scoped narrowly. `mockoauth`, `fakeupstream`, and `smtp-client` are throwaway test infrastructure with no existing test framework in this repo for standalone service binaries — their correctness is verified by the live, browser-driven flow in US1–US3, per the contracts in `contracts/`, not by a unit test suite. The one piece of **shipped application code** this feature touches (`relay_sender.go`'s `tlsClientConfigFor`) gets a proper unit test, matching this project's established convention that every fix ships with a regression test.

**Organization**: Tasks are grouped by user story so each can be verified independently.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete work)
- **[Story]**: US1–US3, mapping to the user stories in spec.md
- Every task names an exact file path, or the exact command being run for verification tasks

## Path Conventions

New, separate Go module at `e2e/` (own `go.mod`, isolated from the main application's dependencies — see plan.md's Structure Decision). Main-module changes are the usual `internal/mockmt/`.

## Story ordering note

All three user stories are P1. **US1 ships first by necessity, not preference** — it is the environment every other story runs inside; US2 and US3 add no new infrastructure, only verification passes against what US1 built. US2 and US3 are otherwise independent of each other and could be done in either order; US3 additionally requires the bundled `relay_sender.go` fix, scoped as its own tests-then-implementation block within that phase since it's the one piece of real application code involved.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: The groundwork every service depends on, with no independent test of its own

- [X] T001 Create `e2e/go.mod` (module `e2e`, go 1.25) and `e2e/go.sum`, requiring `github.com/emersion/go-smtp` and `github.com/emersion/go-sasl` at the same versions pinned in the main module's `go.mod`
- [X] T002 [P] Generate the static self-signed certificate pair at `e2e/certs/fake-upstream-cert.pem` / `e2e/certs/fake-upstream-key.pem` (RSA 2048, 10-year validity, SAN `DNS:fake-upstream,DNS:localhost,IP:127.0.0.1` — the SAN must match `RELAY_HOST` exactly per research R2/R8)
- [X] T003 [P] Add an `e2e` entry to the root `.dockerignore` (research R13 — otherwise every root-context build, including the existing production `docker-compose.yml`, is bloated once `e2e/` exists)

**Checkpoint**: Module skeleton and trust material exist; nothing runnable yet

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The two new services and the test client, shared by every user story below

**⚠️ CRITICAL**: No user story's verification can happen until this phase is complete

- [X] T004 Implement `GET /authorize` in `e2e/mockoauth/main.go` per `contracts/mock-oauth.md`: renders the login form, editable `email` field defaulting to `test@example.com`, preserving `client_id`/`redirect_uri`/`state` as hidden fields
- [X] T005 Implement `POST /authorize` in `e2e/mockoauth/main.go`: issues a random opaque single-use code (`crypto/rand`), stores `code → {email, name, redirect_uri}` in memory, redirects to `<redirect_uri>?code=...&state=...`
- [X] T006 Implement `POST /token` in `e2e/mockoauth/main.go`: looks up and deletes the code, issues a 32-hex-char access token (research R7 — must be well over 8 characters), and **explicitly sets `Content-Type: application/json` before writing the body** (research R6 — the single detail that breaks every login if missed)
- [X] T007 Implement `GET /userinfo` in `e2e/mockoauth/main.go`: validates the bearer token, returns `{sub, email, email_verified, name, picture}` matching `OAuthUserInfo` in `auth.go` exactly
- [X] T008 [P] Write `e2e/mockoauth/Dockerfile`: `golang:1.25-alpine` builder, `alpine:3.20` final stage (keeps `wget` for the healthcheck, research R11), `context: ./e2e`
- [X] T009 Implement `e2e/fakeupstream/main.go` per `contracts/fake-upstream.md`: a `go-smtp` server advertising STARTTLS via the T002 cert/key, `AUTH PLAIN` accepting any credentials, `DATA` read-to-completion with sender/recipients/subject logged to stdout and the body discarded
- [X] T010 [P] Write `e2e/fakeupstream/Dockerfile`: same two-stage pattern as T008 (keeps `nc` for the healthcheck)
- [X] T011 Implement `e2e/smtp-client/send_mail.py`: stdlib `smtplib`/`email` only, fully driven by the environment variables in `data-model.md`'s configuration table; builds the message with only a `To` header and passes any `MAIL_BCC` address solely via the low-level `sendmail(from_addr, to_addrs, msg.as_string())` recipient list, never as a header (research R15)
- [X] T012 Write `docker-compose.e2e.yml` at the repo root: `mock-oauth` (build `./e2e`, port `9000:9000`, healthcheck `wget --spider`), `app` (build root `Dockerfile`, ports `8080:8080`/`1025:1025`, `OAUTH_*` wired per data-model.md, `depends_on: mock-oauth: condition: service_healthy`), `app-relay` (same image, ports `8081:8080`/`1026:1025`, `RELAY_*`/`REVIEWER_EMAILS` set, `RELAY_CA_CERT_FILE` bind-mounting T002's cert read-only, `depends_on` both `mock-oauth` and `fake-upstream` healthy), `fake-upstream` (build `./e2e`, no host port mapping, bind-mounts both T002 files read-only, healthcheck `nc -z localhost 587`), `smtp-client` (`image: python:3-alpine`, bind-mounts `./e2e/smtp-client`, `profiles: ["tools"]` so it never starts on a plain `up`)

**Checkpoint**: `docker compose -f docker-compose.e2e.yml build` succeeds for every service

---

## Phase 3: User Story 1 - Stand up a disposable local environment (Priority: P1) 🎯 MVP

**Goal**: One command produces a fully ready environment; tearing down and restarting always starts clean; nothing about the real deployment is touched.

**Independent Test**: From a clean checkout, run the startup command and confirm every component reports itself ready with no manual account configuration; tear down and bring back up, confirm empty state.

- [X] T013 [US1] Bring up the stack: `docker compose -f docker-compose.e2e.yml up -d --build`; confirm the command does not return until `mock-oauth` and `fake-upstream` report healthy and `app`/`app-relay` have started (FR-001, research R14)
- [X] T014 [US1] Run `docker compose -f docker-compose.e2e.yml ps`; confirm all five service definitions are present with no restart loops and no port-binding errors on 8080/8081/9000/1025/1026
- [X] T015 [US1] Visit `http://localhost:9000/authorize` directly in a browser (no query parameters) and confirm the mock login page renders — sanity-checks T004 standalone before any login flow is layered on top
- [X] T016 [US1] Tear down (`docker compose -f docker-compose.e2e.yml down`) and bring the stack back up; confirm both application instances' inboxes and queues are empty (FR-009) — no message or state carried over from any earlier manual testing during T013–T015
- [X] T017 [US1] Confirm the existing `docker-compose.yml` and its `mockmt` service are unaffected: `docker compose -f docker-compose.yml config` still parses cleanly and references nothing from `docker-compose.e2e.yml` (FR-010)

**Checkpoint**: The environment stands up cleanly, repeatably, and in isolation from production configuration — the foundation every other story runs on

---

## Phase 4: User Story 2 - Verify capture-only mode end to end (Priority: P1)

**Goal**: A developer can sign in, submit mail, and see it rendered — the application's original always-on behavior — using only throwaway identities.

**Independent Test**: Sign in as any throwaway identity, submit a message addressed to it, confirm it renders correctly in the portal.

- [X] T018 [US2] Via the dev-browser skill: navigate to `http://localhost:8080`, click **Sign in with OAuth**, complete the mock login as `test@example.com`, confirm landing authenticated on the Dashboard
- [X] T019 [US2] Run `docker compose -f docker-compose.e2e.yml run --rm smtp-client` (defaults target `app:1025`, addressed to `test@example.com`)
- [X] T020 [US2] Via dev-browser: refresh the Dashboard, confirm the message appears; open it and confirm subject and body render correctly
- [X] T021 [US2] Via dev-browser: sign in as a second, different throwaway identity and confirm that identity's inbox does **not** show the message from T019 (sanity check of the application's existing per-recipient inbox scoping, cheap to confirm here)

**Checkpoint**: Capture-only mode is verified end to end through an ordinary browser session

---

## Phase 5: User Story 3 - Verify relay-with-approval mode end to end (Priority: P1)

**Goal**: A submitted message is held for review, a reviewer sees a blind-carbon recipient correctly flagged, and approving it genuinely relays the message — over a connection whose trust is actually verified, not bypassed.

**Independent Test**: Submit a message with a hidden recipient, sign in as an authorized reviewer, confirm the hidden flag, approve, confirm delivery independently via the stand-in server's own logs.

### Tests for the bundled fix

- [X] T022 [P] [US3] Add a test to `internal/mockmt/relay_sender_test.go` asserting `tlsClientConfigFor` sets `ServerName` to `cfg.Host` when the resolved TLS config has none, and leaves an already-set `ServerName` untouched; confirm it **fails** against the current implementation

### Implementation for the bundled fix

- [X] T023 [US3] Fix `tlsClientConfigFor` in `internal/mockmt/relay_sender.go` (research R2): clone the resolved `tls.Config` (never mutate `cfg.TLSConfig` in place — it is shared across concurrent `relaySend` calls) and set `ServerName: cfg.Host` when empty; confirm T022 now passes
- [X] T024 [US3] Run `go build ./... && go vet ./... && go test ./... -race` in the main module; confirm no regression, matching research R2's confirmation that every existing TLS-handshake test already sets its own `ServerName` and is unaffected

### Verification (depends on Phase 2's compose file and T023's fix)

- [X] T025 [US3] Via dev-browser: navigate to `http://localhost:8081`, sign in via the mock as `reviewer@example.com`
- [X] T026 [US3] Run `docker compose -f docker-compose.e2e.yml run --rm -e SMTP_HOST=app-relay -e MAIL_TO=customer@example.com -e MAIL_BCC=audit@example.com smtp-client`
- [X] T027 [US3] Via dev-browser: open the Review Queue, confirm the message is present; open it and confirm `audit@example.com` is listed and flagged as hidden from the other recipients, and that the body renders
- [X] T028 [US3] Via dev-browser: press **Send Now**; confirm the portal reports the message Sent
- [X] T029 [US3] Run `docker compose -f docker-compose.e2e.yml logs fake-upstream`; confirm a log line independently corroborating receipt (sender/recipients/subject matching T026) — this, not the portal's own report, is what FR-012/SC-006 require

**Checkpoint**: Relay-with-approval mode is verified end to end, backed by a real, hostname-verified TLS handshake rather than one that was silently bypassed

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T030 [P] Write `e2e/README.md`: a short pointer to `quickstart.md` plus the two `docker compose` commands a developer needs to remember
- [X] T031 Tear down (`docker compose -f docker-compose.e2e.yml down`); confirm no leftover containers, networks, or (since none are declared) volumes remain
- [X] T032 Run `make ci` in the main module (fmt-check, vet, lint, test) to confirm the `relay_sender.go` fix and its test are clean by the project's existing standard

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies
- **Foundational (Phase 2)**: depends on Setup — **blocks every user story**
- **US1 (Phase 3)**: depends on Foundational; must complete before US2/US3, since it's the environment they verify against
- **US2 (Phase 4)**: depends on US1's environment being up; independent of US3
- **US3 (Phase 5)**: depends on US1's environment being up; its own fix-first sub-block (T022–T024) has no dependency on US1 at all and could be done earlier, but is grouped here since it exists to serve this story's verification
- **Polish (Phase 6)**: depends on US2 and US3 both being verified

### Parallel Opportunities

- T002, T003 in Setup
- T008, T010 in Foundational (once T004–T007 and T009 respectively are far enough along to know the binaries' entry points — in practice, straightforward to write alongside the `main.go` files themselves)
- T022 can be written and run (to confirm it fails) independent of everything in Phases 1–4
- US2 (Phase 4) and the fix-first sub-block of US3 (T022–T024) have no dependency on each other and can proceed in parallel with a second developer
- T030 alongside T031/T032

---

## Implementation Strategy

### MVP scope

Phases 1–3 (Setup, Foundational, US1). That delivers the entire infrastructure and proves it stands up cleanly and repeatably — the precondition for everything else, and independently valuable even before either verification pass is run.

### Incremental delivery

1. Setup + Foundational → every service builds
2. **US1** → the environment stands up, tears down, and restarts clean. **Stop and validate.**
3. **US2** → capture-only mode proven via browser
4. **US3** → relay-with-approval mode proven via browser, backed by a real TLS fix
5. Polish → teardown hygiene, CI confirmation

### Sequencing risk worth respecting

- **T006's `Content-Type` header is not optional polish — it's load-bearing.** Skipping it doesn't degrade the mock gracefully; it makes every single login attempt fail with a confusing 500, for a reason that has nothing to do with anything else in this feature (research R6). Get this right before debugging anything else that looks like a login failure.
