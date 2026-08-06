# Feature Specification: SMTP Authentication

**Feature Branch**: `001-smtp-authentication`
**Created**: 2026-08-06
**Status**: Draft
**Input**: User description: "read the code and understand the current functionalities. Lets add authentication for SMTP, i believe today its wide open. analyze and ask clarifying questions"

## Current State (as analyzed from the codebase)

- The SMTP server (`internal/mockmt/smtp.go`) accepts connections on `SMTP_PORT` with no encryption and `AllowInsecureAuth` enabled.
- The `Session` type has a legacy `AuthPlain(username, password string) error` method that unconditionally returns `nil` (accepts any credentials) — but the SMTP library version in use (`go-smtp` v0.24.0) does not call this method at all, because authentication requires implementing the `AuthSession` interface (`AuthMechanisms()` + `Auth(mech string)`), which this backend does not implement.
- Net effect: the server never advertises an AUTH capability and never asks for credentials. Any client that can reach the SMTP port can connect and relay mail (`MAIL`/`RCPT`/`DATA`) with **no authentication step at all** — confirming the reported issue.
- The web application has its own, separate authentication system: OAuth2 login + JWT bearer tokens (`internal/mockmt/auth.go`), used only to protect the web UI/API (viewing/deleting inbox emails). There is no password field on the `User` record and no existing credential store that SMTP could reuse as-is.
- The project is explicitly a "mock SMTP server" for test/dev environments (per README), typically run via Docker with environment-variable configuration (`env.example`).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reject mail from unauthenticated senders (Priority: P1)

As the operator of a mockmt instance, I want the SMTP server to refuse to accept mail from any client that has not successfully authenticated, so that my instance cannot be used as an open relay by anyone who can reach the port.

**Why this priority**: This is the core security gap reported — without it, the feature delivers no value. Blocking unauthenticated mail is the minimum viable fix.

**Independent Test**: Point a generic SMTP client at the server without providing credentials and attempt to send a message; the server must reject the attempt (either by not offering the mail transaction to proceed, or by rejecting `MAIL`/`RCPT`) and no email is stored.

**Acceptance Scenarios**:

1. **Given** SMTP authentication is configured and enabled, **When** a client connects and attempts to send mail without authenticating, **Then** the server rejects the transaction with a standard SMTP error response and no email is persisted.
2. **Given** SMTP authentication is configured and enabled, **When** a client attempts to authenticate with an incorrect username or password, **Then** the server rejects the authentication attempt and does not allow the mail transaction to proceed.

---

### User Story 2 - Send mail with valid credentials (Priority: P2)

As a developer/test system sending email through mockmt, I want to authenticate with a configured username and password so my existing test workflows keep working after authentication is enforced.

**Why this priority**: Once unauthenticated mail is blocked, legitimate senders must have a working path, otherwise the tool becomes unusable.

**Independent Test**: Configure valid SMTP credentials, point a standard SMTP client (e.g., the existing `cmd/test_email` tool or any mail library) at the server using those credentials, and confirm the email is accepted and appears in the recipient's inbox exactly as it does today.

**Acceptance Scenarios**:

1. **Given** valid SMTP credentials are configured, **When** a client authenticates with the correct username and password and sends a message, **Then** the server accepts the mail transaction and the email is stored and viewable in the web inbox as before.
2. **Given** a client has an already-authenticated session, **When** it sends multiple messages within that session, **Then** each message is accepted without re-authenticating per message.

---

### User Story 3 - Operator configures SMTP credentials (Priority: P3)

As the operator deploying mockmt (e.g., via Docker), I want to set the SMTP username and password through configuration (environment variables), consistent with how other secrets (OAuth client secret, JWT secret) are configured today, so I can manage credentials without code changes.

**Why this priority**: Needed for the feature to be usable in real deployments, but the mechanism itself (env var configuration) follows an existing, well-understood pattern in this codebase, so it's lower risk/priority than P1/P2.

**Independent Test**: Set the credential configuration values, start the server, and confirm the values in effect match what was configured (verified indirectly through User Story 1 and 2 tests passing with those exact values).

**Acceptance Scenarios**:

1. **Given** an operator sets SMTP username/password configuration before starting the server, **When** the server starts, **Then** it enforces authentication using those exact credentials.

---

### Edge Cases

- What happens when the server is started with no SMTP credentials configured? The server MUST fail to start with a clear fatal configuration error — there is no fallback to open/unauthenticated behavior (authentication is mandatory, not opt-in).
- How does the system handle a client that never issues `AUTH` before `MAIL FROM`? It must be rejected the same as a failed authentication attempt.
- How does the system handle repeated failed authentication attempts from the same client/connection? At minimum these must be logged; no lockout/rate-limiting behavior is required for this feature (see Assumptions).
- What happens to the existing `cmd/test_email` helper script and documented `docker run` examples once auth is required? They must be updated to include credentials so they keep working.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The SMTP server MUST require clients to authenticate before it will accept a mail transaction (`MAIL FROM` / `RCPT TO` / `DATA`).
- **FR-002**: The SMTP server MUST reject `MAIL FROM` (or any attempt to start a mail transaction) from a session that has not successfully authenticated, with a standard SMTP error response.
- **FR-003**: The SMTP server MUST validate presented credentials against the configured SMTP username and password and MUST reject authentication attempts with incorrect credentials.
- **FR-004**: The system MUST allow the SMTP username and password to be configured by the operator via environment variables, following the existing configuration pattern used for OAuth and JWT settings.
- **FR-005**: The system MUST fail to start (with a clear error message) if SMTP authentication is enabled/required but no credentials are configured, rather than silently allowing unauthenticated access.
- **FR-006**: The system MUST log successful and failed SMTP authentication attempts (without logging the plaintext password) to support auditing of who is sending mail through the server.
- **FR-007**: The system MUST continue to support the existing email-processing behavior (parsing, storage, inbox display) unchanged for mail sent by an authenticated, authorized sender.
- **FR-008**: The project's example configuration (`env.example`), README usage instructions, and the `cmd/test_email` helper MUST be updated to reflect the new required SMTP credentials so documented workflows keep working.
- **FR-009**: SMTP credential validation MUST use a single set of shared operator-configured credentials (not per-user credentials tied to individual web-app accounts), consistent with this tool's role as a single-tenant test instance.
- **FR-010**: SMTP authentication MUST be mandatory: the server MUST refuse to start (failing fast with a clear configuration error, per FR-005) unless SMTP credentials are configured, regardless of whether this is a fresh deployment or an upgrade of an existing one. There is no opt-out that preserves the previous open-relay behavior.
- **FR-011**: The system MUST support authenticating over the existing unencrypted connection (plaintext `AUTH`, consistent with today's `AllowInsecureAuth` setting). Requiring STARTTLS/TLS before authentication is out of scope for this feature.

### Key Entities

- **SMTP Credential**: The operator-configured username and password required to authenticate an SMTP session. Not tied to a specific web-app `User` record; a single shared secret for the instance.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of mail-send attempts that do not include valid credentials are rejected by the server (zero unauthenticated emails accepted or stored).
- **SC-002**: 100% of mail-send attempts using the correct configured credentials are accepted and appear in the recipient's inbox, matching current (pre-change) delivery behavior.
- **SC-003**: An operator can go from a fresh deployment to a working, authenticated send in the time it takes to set two configuration values and restart the server (no code changes required).
- **SC-004**: Every authentication attempt (success or failure) against the SMTP server is discoverable in the server's logs after the change, where none were recorded before.

## Clarifications

### Session 2026-08-06

- Q: How should SMTP credentials be modeled? → A: Single shared operator-configured username/password (env vars), not per-user credentials, not OAuth/XOAUTH2.
- Q: Should authentication be enforced by default, or only when credentials are explicitly configured? → A: Mandatory — the server always requires configured credentials and refuses to start otherwise; no opt-out preserves the old open behavior.
- Q: Is plaintext AUTH over the current unencrypted connection acceptable, or is TLS required first? → A: Plaintext AUTH remains acceptable; requiring STARTTLS/TLS is out of scope for this feature.

## Assumptions

- This is a single-tenant "mock" test tool; a single shared SMTP username/password configured by the operator is the credential model (confirmed via clarification).
- Rate limiting, account lockout, and brute-force protection for repeated failed SMTP auth attempts are out of scope for this feature; logging failed attempts is sufficient.
- The web application's OAuth/JWT authentication system is out of scope for this feature and is not being changed; SMTP authentication is a separate, independent credential.
- Existing stored emails and the email schema are unaffected by this change; only the SMTP acceptance path changes.
- The existing `cmd/test_email` helper, `README.md` usage examples, and `env.example` are considered part of this feature's scope to keep the documented workflow functional, but are not user-facing product requirements beyond staying accurate.
- Enforcing mandatory authentication is a deliberate breaking change for any existing deployment that has not set SMTP credentials; such deployments will fail to start until credentials are configured. This is considered acceptable per clarification.
