# Implementation Plan: Local End-to-End Test Environment

**Branch**: `003-e2e-docker-testrig` | **Date**: 2026-08-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/003-e2e-docker-testrig/spec.md`

## Summary

A `docker compose` stack that substitutes every external dependency the application needs to be fully exercised — a real OAuth identity provider, a real upstream mail account — with disposable, developer-controlled stand-ins, so both operating modes (capture-only and relay-with-approval) can be verified locally with one command and no real accounts.

Three new pieces, two of them new Go programs sharing one small `e2e/go.mod` kept entirely separate from the shipped application's module:

- **A mock OAuth server** (`e2e/mockoauth`) implementing exactly the authorization-code flow the app actually performs — verified line-by-line against `auth.go` and `golang.org/x/oauth2`'s client internals, not against any real provider's spec. One detail is load-bearing: the `/token` response must set `Content-Type: application/json` explicitly, or Go's content-sniffing routes it into the wrong parser and every login fails (research R6).
- **A stand-in destination server** (`e2e/fakeupstream`) using the same `go-smtp` library the app already depends on, because the app's own capture-only SMTP server structurally cannot serve as a relay target — it never advertises `STARTTLS`, and the relay client requires TLS unconditionally (research R1).
- **A non-interactive SMTP test client** (`e2e/smtp-client`, Python stdlib only) parameterized by environment variables, careful to never let a `Bcc:` header reach the wire so the hidden-recipient verification is a faithful envelope-vs-header check (research R15).

All four services (the two above, plus **two instances** of the existing application image — one per mode, since a running instance can't switch modes without a restart) are wired together in a new `docker-compose.e2e.yml`, kept separate from the existing production-style `docker-compose.yml`.

**One real application bug fix is bundled in**, discovered while designing this: `relay_sender.go` never sets `tls.Config.ServerName`, so hostname verification is silently skipped on every relay connection today (confirmed against the Go standard library source, research R2). This feature's own success criteria (the relay verification must exercise genuine trust-checking) would be meaningless without fixing it, so the fix ships with this feature rather than as a separate one.

## Technical Context

**Language/Version**: Go 1.25 for the two new server programs (matching the main module); Python 3 (stdlib only, no pip dependencies) for the test client
**Primary Dependencies**: `github.com/emersion/go-smtp` + `github.com/emersion/go-sasl` for `fakeupstream` (same pinned versions as the main module: v0.24.0 / the pinned go-sasl commit); Go stdlib only (`net/http`, `crypto/rand`, `html/template`) for `mockoauth`; Python stdlib only (`smtplib`, `email`) for the test client. **Zero new dependencies added to the shipped application's `go.mod`** — `e2e/go.mod` is a fully separate module.
**Storage**: None. Two in-memory maps in `mockoauth` (authorization codes, access tokens), both single-use/ephemeral. No database, no volumes.
**Testing**: `cd e2e && go build ./...` as a standalone compile check (separate module, not touched by the main module's `go build ./...`/`go vet ./...`/`go test ./...`); one new Go unit test in the main module for the `tlsClientConfigFor` fix; the environment itself is verified via a live dev-browser session per `quickstart.md`, not a checked-in automated test suite (spec's resolved scope).
**Target Platform**: Local developer machine, via `docker compose`; Docker confirmed available and used during planning to verify Alpine's BusyBox toolset and Python's SMTP AUTH fallback empirically rather than assumed.
**Project Type**: Web application (existing) + new test-infrastructure directory (`e2e/`) with its own module boundary.
**Performance Goals**: SC-001's 5-minute cold-start budget; otherwise none — this is point-in-time verification tooling, not a service with load characteristics.
**Constraints**: The startup command must not return until every component is genuinely ready (resolved via clarification — implemented as Compose health checks + `depends_on: condition: service_healthy`, R14). No plaintext relay path may be introduced anywhere in this design (matches feature 002's existing constraint). The two new Dockerfiles must not require access to the main application's source (context is `./e2e`, not repo root, per research R13).
**Scale/Scope**: Single developer, single machine, one test message per verification pass per mode. Four running services plus one on-demand, non-persistent client invocation.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` remains the unratified template — no project-specific principles are defined for this repository, consistent with features 001 and 002. This plan holds itself to the same observed conventions:

| Convention (as observed) | How this plan follows it |
|---|---|
| No new dependencies unless necessary | Zero new deps in the shipped app's `go.mod`; `e2e/go.mod` reuses libraries and pinned versions the main module already vetted |
| Fail fast / verify against real behavior, not assumption | Every design decision in `research.md` is backed by reading actual source (Go stdlib, `golang.org/x/oauth2`, the app's own code) or a live check, not asserted from memory |
| Existing production artifacts are not disturbed by new work | `docker-compose.yml` and the root `Dockerfile` are untouched; the only shared-surface edit is one line added to `.dockerignore` |
| Every code fix carries a regression test | The `tlsClientConfigFor` fix ships with a new unit test (see Complexity Tracking) |

**Gate status: PASS (no constitution constraints defined).**

**Post-design re-check (after Phase 1)**: Design introduces no new dependencies to the shipped application, no schema, and confines all new Go code to a separately-mooted module. The one change to existing application code (`relay_sender.go`) is small, isolated, and independently justified by research R2 regardless of this feature. **Gate status: PASS.**

## Project Structure

### Documentation (this feature)

```text
specs/003-e2e-docker-testrig/
├── spec.md                      # Feature specification (with Clarifications)
├── plan.md                      # This file
├── research.md                  # Phase 0 output — 15 decisions, each independently verified
├── data-model.md                # Phase 1 output — component map, configuration surface
├── quickstart.md                # Phase 1 output — bring up, verify both modes, tear down
├── contracts/
│   ├── mock-oauth.md            # Phase 1 output — exact HTTP behavior required
│   └── fake-upstream.md         # Phase 1 output — exact SMTP behavior required
├── checklists/
│   └── requirements.md          # Spec quality checklist (all items pass)
└── tasks.md                     # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
mockmt/
├── internal/mockmt/
│   ├── relay_sender.go          # MODIFIED: tlsClientConfigFor sets ServerName when absent (research R2)
│   └── relay_sender_test.go     # MODIFIED: new test for the ServerName fix
├── .dockerignore                # MODIFIED: add `e2e` entry (research R13)
├── docker-compose.e2e.yml       # NEW: the four-service (plus one on-demand) test stack
└── e2e/                         # NEW: separate Go module, isolated from the shipped app
    ├── go.mod, go.sum
    ├── certs/
    │   ├── fake-upstream-cert.pem   # static, checked-in, self-signed, 10-year validity
    │   └── fake-upstream-key.pem
    ├── mockoauth/
    │   ├── main.go
    │   └── Dockerfile               # golang:1.25-alpine builder -> alpine:3.20 final (keeps wget for healthcheck)
    ├── fakeupstream/
    │   ├── main.go
    │   └── Dockerfile               # same two-stage pattern (keeps nc for healthcheck)
    └── smtp-client/
        └── send_mail.py             # stdlib-only; bind-mounted into python:3-alpine, no Dockerfile needed
```

**Structure Decision**: The two new Go programs live in a sibling `e2e/` module rather than under the main module's `cmd/` — they are test-only tooling that must never be reachable from `go build ./...`/`go vet ./...`/`golangci-lint run ./...` at the repo root, and must never cause the shipped application's `go.mod`/`go.sum` to gain a single new entry. This mirrors how `cmd/test_email` already lives inside the main module (it ships alongside the app and imports nothing new), while these two programs are categorically different: they exist only to stand in for infrastructure the shipped application will never itself provide.

## Complexity Tracking

> One deviation from "this feature's changes stay inside its own new files," recorded since it touches existing, shipped application code.

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Modify `internal/mockmt/relay_sender.go` (`tlsClientConfigFor`) and add a test to `relay_sender_test.go` | Research R2: TLS hostname verification is silently skipped on every relay connection today. Without this fix, the stand-in destination server this feature builds would "pass" verification even presenting a certificate for the wrong host, making FR-006/SC-005 (the relay verification must exercise genuine trust-checking) unsatisfiable by construction — there would be nothing genuine to verify against. | *Build the environment without fixing this, and let the relay verification silently rely on the (broken) skip-verification behavior* — rejected because it would make this feature's own security-relevant success criterion false while appearing to pass, exactly the kind of gap a review would later catch and require redoing this work to fix. *File it as a fully separate, later feature* — rejected because it is a one-function, independently-tested, five-line fix uncovered *by* this work, and shipping the e2e rig without it means the rig can never actually prove the thing it claims to prove. |

No other deviations. All other new work is additive: a new module, a new compose file, one new `.dockerignore` line.
