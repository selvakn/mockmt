# Phase 1 Data Model: SMTP Authentication

## Overview

This feature introduces no new persisted/database entities — it does not touch the SQLite schema (`users`, `emails` tables) at all. It introduces one new **configuration-time** concept and extends one existing in-memory type.

## SMTP Credential (configuration, not persisted)

Represents the single shared secret an SMTP client must present to send mail through this instance.

| Field    | Type   | Source                    | Notes                                                                 |
|----------|--------|---------------------------|------------------------------------------------------------------------|
| Username | string | `SMTP_USERNAME` env var   | Required; server fails to start if empty (FR-005, FR-010).            |
| Password | string | `SMTP_PASSWORD` env var   | Required; server fails to start if empty. Never logged (FR-006).      |

- **Lifecycle**: Read once at server startup (`StartSMTPServer`); held in memory for the lifetime of the process. Not stored in the database, not exposed via any API, not tied to the `users` table.
- **Validation rule**: Both `Username` and `Password` must be non-empty for the server to start (per FR-005/FR-010 — authentication is mandatory with no opt-out).
- **Relationships**: None to existing entities (`User`, `Email`). This is intentionally a single, global credential independent of the web app's per-user `User` records (per spec clarification / FR-009).

## Session (existing type, extended)

`internal/mockmt/smtp.go`'s `Session` struct gains one field to track authentication state for the lifetime of a single SMTP connection:

| Field         | Type     | Existing/New | Notes                                                                                   |
|---------------|----------|---------------|------------------------------------------------------------------------------------------|
| `from`        | string   | Existing      | Unchanged.                                                                                |
| `to`          | []string | Existing      | Unchanged.                                                                                |
| `authenticated` | bool   | **New**       | Set `true` only after a successful `AUTH PLAIN` exchange; checked at the start of `Mail()`. Not reset by `Reset()` (an authenticated connection may send multiple messages). |

**State transitions**:

```
[Connected, authenticated=false]
        │
        │ AUTH PLAIN <valid credentials>
        ▼
[authenticated=true] ──── Mail/Rcpt/Data (any number of messages) ────▶ [authenticated=true]
        │
        │ AUTH PLAIN <invalid credentials> (may retry)
        ▼
[authenticated=false] (unchanged; SMTPError 535 returned to client)

[authenticated=false] ──── Mail/Rcpt/Data ────▶ rejected (SMTPError 502, ErrAuthRequired)
```

No other entities, tables, or migrations are introduced by this feature.
