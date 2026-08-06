# Implementation Plan: SMTP Authentication

**Branch**: `001-smtp-authentication` | **Date**: 2026-08-06 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-smtp-authentication/spec.md`

**Note**: This template is filled in by the `/speckit-plan` command. See `.specify/templates/plan-template.md` for the execution workflow.

## Summary

The SMTP server currently accepts mail from any client with no authentication at all (the existing `AuthPlain` method is dead code never invoked by the `go-smtp` v0.24.0 library, which requires implementing the `AuthSession` interface to advertise `AUTH`). This plan implements mandatory `AUTH PLAIN` authentication against a single, operator-configured shared username/password (`SMTP_USERNAME`/`SMTP_PASSWORD` env vars), rejects any mail transaction on unauthenticated connections, fails the server startup fast if credentials aren't configured, logs auth attempts (without passwords), and updates `env.example`, `README.md`, and `cmd/test_email` so documented workflows keep working. Plaintext AUTH over the existing unencrypted connection remains acceptable (no TLS/STARTTLS work in scope).

## Technical Context

**Language/Version**: Go 1.25 (per `go.mod`)
**Primary Dependencies**: `github.com/emersion/go-smtp` v0.24.0 (SMTP server), `github.com/emersion/go-sasl` (already an indirect dependency; provides `sasl.NewPlainServer` for `AUTH PLAIN`), `github.com/joho/godotenv` (env loading, existing)
**Storage**: SQLite via `github.com/mattn/go-sqlite3` — unaffected by this feature (no schema changes)
**Testing**: `go test` (standard library `testing`); no test files currently exist in the repo, this feature adds the first ones for `internal/mockmt`
**Target Platform**: Linux server (Docker container per `Dockerfile`/`docker-compose.yml`), also runs locally via `go run .`
**Project Type**: Web service (Go backend + Vue.js frontend) — this feature is backend-only (SMTP protocol layer); no frontend changes
**Performance Goals**: N/A — mock/test tool, low-volume by design; no new performance targets introduced
**Constraints**: No new external dependencies (must reuse `go-sasl`, already present transitively); no TLS/STARTTLS work (per spec clarification); no changes to the database schema or web API/auth
**Scale/Scope**: Single-tenant instance, single shared credential pair; change is localized to `internal/mockmt/smtp.go` (+ new small config helper), `env.example`, `README.md`, `cmd/test_email/main.go`

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

`.specify/memory/constitution.md` is still the unfilled template (no project-specific principles have been ratified for this repository). There are no defined gates to evaluate against. This plan instead holds itself to the general engineering conventions already evident in the codebase (env-var-driven configuration, `log`-based logging, no new dependencies unless necessary, fail-fast on misconfiguration matching `InitDatabase`/`StartSMTPServer` error handling in `main.go`). **Gate status: PASS (no constitution constraints defined).**

**Post-design re-check (after Phase 1)**: Design in `research.md`/`data-model.md`/`contracts/` introduces zero new dependencies, zero schema changes, and follows existing code conventions throughout. **Gate status: PASS.**

## Project Structure

### Documentation (this feature)

```text
specs/001-smtp-authentication/
├── plan.md                          # This file (/speckit-plan command output)
├── research.md                      # Phase 0 output
├── data-model.md                    # Phase 1 output
├── quickstart.md                    # Phase 1 output
├── contracts/
│   └── smtp-auth-protocol.md        # Phase 1 output: SMTP protocol behavior contract
└── tasks.md                         # Phase 2 output (/speckit-tasks command)
```

### Source Code (repository root)

This repository is an existing single-project web application (Go backend + Vue.js frontend, per `README.md`'s Tech Stack). This feature is backend-only; the existing layout is reused as-is, no new top-level directories:

```text
mockmt/
├── main.go                         # unchanged
├── internal/mockmt/
│   ├── smtp.go                     # MODIFIED: implement AuthSession, gate Mail(), remove dead AuthPlain
│   ├── smtp_test.go                # NEW: unit tests for auth gating/success/failure
│   ├── utils.go                    # MODIFIED (or new config.go): read/validate SMTP_USERNAME/SMTP_PASSWORD
│   ├── auth.go                     # unchanged (separate web OAuth/JWT system, out of scope)
│   ├── database.go                 # unchanged
│   └── web.go                      # unchanged
├── cmd/test_email/main.go          # MODIFIED: authenticate when sending the test email
├── env.example                     # MODIFIED: document SMTP_USERNAME/SMTP_PASSWORD
├── README.md                       # MODIFIED: update docker run example + usage docs
└── frontend/                       # untouched — no UI changes in this feature
```

**Structure Decision**: Single existing Go module, no new packages. All logic changes live in `internal/mockmt` (primarily `smtp.go`), consistent with how the existing OAuth/JWT auth logic lives in `auth.go` alongside the rest of the package rather than in a separate module.

## Complexity Tracking

*No constitution violations — section intentionally left empty (no gates were violated; see Constitution Check above).*
