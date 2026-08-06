# Phase 0 Research: SMTP Authentication

## Context

The feature adds mandatory authentication to the existing SMTP server (`internal/mockmt/smtp.go`), built on `github.com/emersion/go-smtp` v0.24.0. There are no items left as "NEEDS CLARIFICATION" in the Technical Context (this is a small, self-contained change to an existing Go codebase with an established stack), but several implementation-approach decisions still needed investigation into the SMTP/SASL library APIs actually available. Findings below.

## Decision: How to make the server require and check authentication

**Decision**: Implement the `smtp.AuthSession` add-on interface (`AuthMechanisms() []string` + `Auth(mech string) (sasl.Server, error)`) on the existing `Session` type, and reject `Mail()` calls on sessions that have not completed authentication by returning `smtp.ErrAuthRequired`.

**Rationale**: `go-smtp` v0.24.0 only advertises and processes the `AUTH` SMTP extension for backends whose `Session` implements `AuthSession`. The server has no global "require auth" switch — enforcement is done in application code by (a) implementing `AuthSession` so `AUTH` is offered, and (b) tracking authentication state per-session and checking it at the start of the mail transaction (`Mail`). This is exactly the point identified in the current-state analysis: the existing `AuthPlain` method is legacy/unused dead code because it doesn't satisfy `AuthSession`, so `AUTH` was never advertised and nothing ever checked auth state.

**Alternatives considered**:
- *Rely on a server-level config flag*: Rejected — `smtp.Server` has no such flag; enforcement must be in the `Session`/backend, confirmed via `go doc`.
- *Keep the legacy `AuthPlain` method*: Rejected — dead code, never invoked by this library version; must be removed to avoid confusion.

## Decision: SASL mechanism to support

**Decision**: Support `PLAIN` only, using `github.com/emersion/go-sasl`'s `sasl.NewPlainServer(authenticator)` helper (`go-sasl` is already an indirect dependency of `go-smtp`).

**Rationale**: `PLAIN` is universally supported by SMTP clients/libraries (including Go's standard `net/smtp` via `smtp.PlainAuth`, and the project's own `cmd/test_email` helper) and `go-sasl` provides a ready-made `Server` implementation, so no custom SASL parsing is needed. `go-sasl` does not ship a built-in `LOGIN` server helper, and there's no requirement to support it — `PLAIN` is sufficient for the mock tool's use case (agreed in the spec's transport-security clarification: plaintext AUTH is acceptable, so there's no benefit to adding `LOGIN` as well).

**Alternatives considered**:
- *`LOGIN` mechanism*: Rejected for v1 — not built into `go-sasl`, and would require hand-rolled challenge/response handling for no functional benefit over `PLAIN` given the single-credential model.
- *`CRAM-MD5` / challenge-response mechanisms*: Rejected — unnecessary complexity for a mock/test tool where plaintext auth was explicitly accepted as sufficient.

## Decision: Credential configuration

**Decision**: Two new environment variables, `SMTP_USERNAME` and `SMTP_PASSWORD`, read via the existing `getEnv(key, defaultValue)` helper but with **no default value** — an empty/unset value is treated as "not configured."

**Rationale**: Matches the existing configuration pattern in the codebase (`OAUTH_CLIENT_ID`, `JWT_SECRET_KEY`, etc., all env-var driven, read in `initAuth()`/`getEnv`). Per the spec's resolved clarification, authentication is mandatory with no opt-out, so the startup path must treat a missing username or password as a fatal configuration error (`log.Fatal`), matching how `InitDatabase`/`StartSMTPServer` errors already cause `main.go` to exit today.

**Alternatives considered**:
- *Single combined `SMTP_CREDENTIALS=user:pass` variable*: Rejected — inconsistent with existing convention of separate, clearly-named variables per secret.
- *Config file*: Rejected — no existing config-file mechanism in the project; would be a new pattern for no added benefit.

## Decision: Per-connection authentication state

**Decision**: Add an `authenticated bool` field to the `Session` struct, set to `true` only after the SASL `PLAIN` authenticator callback succeeds, and checked (returning `smtp.ErrAuthRequired` if `false`) at the top of `Mail()`. `Reset()` must NOT clear `authenticated` (an authenticated connection may send multiple messages, per spec User Story 2 Scenario 2); `Logout()` ends the session as today.

**Rationale**: `go-smtp` creates one `Session` per connection (`NewSession(c *smtp.Conn)`), so per-connection state naturally lives on the `Session` struct. Checking at `Mail()` (rather than trying to gate the connection itself) is the standard pattern for this library and directly satisfies FR-001/FR-002.

**Alternatives considered**:
- *Checking in `Rcpt()` or `Data()` instead of `Mail()`*: Rejected — rejecting as early as possible (`Mail`) gives clearer, faster feedback to the client and avoids doing any work for a doomed transaction.

## Decision: Logging approach

**Decision**: Log one line on successful authentication (`log.Printf("SMTP auth succeeded: user=%s", username)`) and one line on failed authentication (`log.Printf("SMTP auth failed: user=%s", username)`), reusing the existing `log` package already used throughout the codebase (`auth.go`, `smtp.go`, `web.go`). Never log the password.

**Rationale**: Matches existing logging style/library in the project (plain `log.Printf`, no structured logging framework present) and satisfies FR-006/SC-004 without introducing a new dependency.

**Alternatives considered**:
- *Structured logging (e.g., slog/zap)*: Rejected — out of scope; no structured logging exists anywhere else in the codebase, so introducing it just for this feature would be inconsistent and unjustified scope creep.

## Summary of resolved unknowns

No open "NEEDS CLARIFICATION" items remain for Phase 1. All decisions above are implementable with dependencies already present in `go.mod`/`go.sum` (`go-smtp`, `go-sasl`) — no new dependencies are required.
