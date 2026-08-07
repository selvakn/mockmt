# Contract: Stand-In Destination Server

**Feature**: `003-e2e-docker-testrig` | **Date**: 2026-08-07

Defines the exact SMTP behavior `e2e/fakeupstream` must implement. Built on `github.com/emersion/go-smtp` — the same library, same pinned version, the main application already depends on — so its behavior is a real SMTP server, not a hand-rolled protocol approximation.

## Session behavior

| Step | Behavior |
|---|---|
| Connection | Plain TCP; `STARTTLS` advertised in `EHLO` (research R1: this is the one thing the app's own capture-only server cannot do, which is why this program exists at all) |
| `STARTTLS` | Upgrades using the checked-in static certificate + private key (`e2e/certs/fake-upstream-{cert,key}.pem`) |
| `AUTH PLAIN` | Accepted unconditionally, any username/password — this is a delivery double, not an authentication boundary; the credentials it's actually given (`RELAY_USERNAME`/`RELAY_PASSWORD`) are meaningless to it |
| `MAIL FROM` / `RCPT TO` | Always accepted |
| `DATA` | Read to completion, parsed enough to log `From`, `To` (envelope recipients), and `Subject`; body discarded, nothing written to disk |
| Logging | One line per completed message to stdout — this is the "independent evidence" FR-012/SC-006 require, checked via `docker compose logs fake-upstream`, deliberately outside the application's own UI |

## Certificate

Static, checked-in, self-signed, generated once (not regenerated at build or run time — research R8 confirms neither Dockerfile needs it baked in, since both containers receive it via a runtime bind mount):

- Subject/SAN: `DNS:fake-upstream` (the Compose service's own internal DNS name — this must match `RELAY_HOST` on the `app-relay` side, since the `ServerName` fix (research R2) means hostname verification is now real and will reject a mismatch), plus `DNS:localhost` and `IP:127.0.0.1` as a convenience for anyone running the binary standalone outside Compose.
- Validity: 10 years — long enough that regeneration is never a maintenance concern for a test fixture.
- Key: RSA 2048, matching the pattern already used by the Go test suite's own `generateSelfSignedCert` helper (`internal/mockmt/relay_testsupport_test.go`) for consistency, though this is a separate, independently generated pair — nothing is shared between the test suite and this environment.

## Healthcheck

`nc -z localhost <port>` — a bare TCP connect, sufficient to prove the listener is up (research R11); no SMTP-level probe needed, since the app doesn't dial this server at its own startup either (research R14) and the only thing gating the Compose `depends_on` is "is anything listening yet."

## Explicitly not implemented

- Any actual mail storage, forwarding, or bounce handling.
- Rejecting specific recipients or simulating partial failure (feature 002's own test suite already covers that against its in-process fake upstream; this is a real, standalone Docker service for a different purpose — human-visible, browser-driven verification, not Go unit tests).
- Any inspection API — `docker compose logs` is the intended, sufficient inspection mechanism (research R14, data-model.md).
