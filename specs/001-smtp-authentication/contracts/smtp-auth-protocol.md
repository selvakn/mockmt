# Contract: SMTP Authentication Protocol Behavior

This is the external interface this feature exposes: the observable SMTP protocol behavior that any client (test scripts, mail libraries, `cmd/test_email`) can depend on.

## Capability advertisement

- On connecting, the server MUST advertise the `AUTH` extension with mechanism `PLAIN` in response to `EHLO`.
- Prior to this feature, `AUTH` was not advertised at all.

## Authentication exchange

| Client action | Server response |
|---|---|
| `AUTH PLAIN <base64(authzid\0username\0password)>` with correct configured username/password | `235 2.7.0 Authentication successful` (via `go-smtp`/`go-sasl` default success handling) |
| `AUTH PLAIN <base64(...)>` with incorrect username or password | `535 5.7.8 Authentication failed` (`smtp.ErrAuthFailed`) |
| `AUTH` with an unsupported/unknown mechanism | `504 5.7.4 Unsupported authentication mechanism` (`smtp.ErrAuthUnknownMechanism`) |

## Mail transaction gating

| Client state | Client action | Server response |
|---|---|---|
| Not authenticated | `MAIL FROM:<addr>` | `502 5.7.0 Please authenticate first` (`smtp.ErrAuthRequired`); transaction does not proceed, no email is stored |
| Authenticated (this connection) | `MAIL FROM:<addr>` → `RCPT TO:<addr>` → `DATA` | Accepted; email parsed and stored exactly as before this feature (unchanged behavior downstream of `Mail`/`Rcpt`/`Data`) |
| Authenticated (this connection) | A second (or further) full mail transaction on the same connection | Accepted without re-authenticating |

## Startup contract

| Configuration state | Server behavior |
|---|---|
| `SMTP_USERNAME` and `SMTP_PASSWORD` both set (non-empty) | Server starts normally; SMTP AUTH enforced as above |
| Either `SMTP_USERNAME` or `SMTP_PASSWORD` missing/empty | Server MUST fail to start with a clear, actionable fatal log message (e.g., naming the missing variable); no listener is opened |

## Explicitly out of scope for this contract

- STARTTLS/TLS negotiation — unchanged (not required before `AUTH`, per spec clarification).
- `LOGIN`, `CRAM-MD5`, or any mechanism other than `PLAIN`.
- Per-recipient or per-sender authorization beyond "connection is authenticated" (no ACLs on which `MAIL FROM`/`RCPT TO` values an authenticated user may use).
- Rate limiting / connection throttling / lockout after repeated failed `AUTH` attempts.
