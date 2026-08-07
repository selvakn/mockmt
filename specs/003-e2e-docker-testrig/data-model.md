# Phase 1 Data Model: Local End-to-End Test Environment

**Feature**: `003-e2e-docker-testrig` | **Date**: 2026-08-07 | **Plan**: [plan.md](./plan.md)

This feature is test infrastructure, not a data-bearing product feature — there is no persistent schema. What follows maps the spec's Key Entities to concrete components, their configuration surface, and the one piece of genuine in-memory state (the mock OAuth server's session bookkeeping).

## Component map

```text
Developer's browser
      │
      ├──────────────► app          (capture-only, :8080 web / :1025 SMTP)
      │                     │
      ├──────────────► app-relay    (relay-with-approval, :8081 web / :1026 SMTP)
      │                     │  (RELAY_HOST, over TLS)
      │                     ▼
      │               fake-upstream  (stand-in destination server, :587 internal only)
      │
      └──────────────► mock-oauth    (:9000, browser-reachable + internal DNS)
                              ▲
              app / app-relay ┘ (token + userinfo exchange, server-to-server)

smtp-client (on-demand, not a long-running service)
      ──────────────► app:1025  or  app-relay:1025
```

## `Local Test Environment` → the Compose project as a whole

Not a single component but the unit `docker compose up` / `down` acts on. No fields; its only behavioral contract is FR-001/FR-009: the up command blocks until every component below reports itself ready (R14), and every down-then-up cycle starts empty (R9 — true automatically, no explicit reset step needed).

## `Stand-In Identity Provider` → `mock-oauth` service

| Aspect | Value |
|---|---|
| Reachability | Browser-facing (host-mapped port) **and** server-to-server (internal Compose DNS) — the same service, two routes in |
| State | In-memory only, two maps, no persistence: `code → {email, name, redirect_uri}` (single-use, deleted on redemption) and `access_token → {email, name}` |
| Identity input | A free-text field the developer fills in at the mock's own login page — any string they choose becomes the signed-in identity (spec's `Throwaway Identity`) |
| Validation performed | None on `client_id`/`client_secret` — this is a disposable double, not a security boundary |

**Endpoints** (contract detail in `contracts/mock-oauth.md`): `GET /authorize`, `POST /token`, `GET /userinfo`.

## `Test Mail Sender` → `smtp-client` one-shot invocation

Not a running service — invoked on demand (`docker compose run --rm smtp-client`), once per verification pass per app instance. Configuration is entirely environment-variable driven (FR-004's required fields map directly):

| Variable | Maps to spec requirement |
|---|---|
| `SMTP_HOST`, `SMTP_PORT` | which app instance to submit into |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | the app's existing inbound `AUTH PLAIN` credentials (feature 002) |
| `MAIL_FROM` | sender |
| `MAIL_TO` | visible recipient(s) |
| `MAIL_BCC` (optional) | the recipient hidden from the others (FR-004, FR-011) — never emitted as a header (R15) |
| `MAIL_SUBJECT`, `MAIL_BODY` | message content |
| `MAIL_ATTACHMENT_PATH` (optional) | exercises attachment rendering incidentally, not a named requirement but free given the app already supports it |

No state carried between invocations.

## `Stand-In Destination Server` → `fake-upstream` service

| Aspect | Value |
|---|---|
| Reachability | Internal Compose DNS only (`fake-upstream`) — never host-mapped; only `app-relay` ever dials it |
| Identity | A static, checked-in, self-signed certificate + private key (`e2e/certs/`), SAN covering the service's own Compose DNS name |
| Trust anchor | The certificate's public half is bind-mounted into `app-relay` as `RELAY_CA_CERT_FILE` — the two containers share a trust relationship established entirely through files, no runtime cert exchange |
| Behavior | Advertises STARTTLS, accepts `AUTH PLAIN` unconditionally (any credentials — this is a destination double, not an auth boundary), reads and discards `DATA`, logs one line per message received (sender/recipients/subject) to stdout |
| Independent verification | `docker compose logs fake-upstream` is the "independent evidence" FR-012/SC-006 require — confirmation that does not come from the app's own UI |

## `Throwaway Identity`

Not a stored entity — a value that exists only as long as a developer types it into the mock OAuth login page and it flows through to a JWT. Two roles it plays, both resolved entirely by the *application's* existing configuration (nothing new this feature defines):

- An **ordinary identity**, used against `app`, whose only property that matters is "whatever address the test message was addressed to."
- A **reviewer identity**, used against `app-relay`, which must appear in that instance's `REVIEWER_EMAILS` to see the review queue at all (feature 002's existing access rule, unchanged).

## Configuration surface (the closest thing to a schema this feature has)

All environment variables new to this feature, none of which are read by any code outside `e2e/`:

| Variable | Consumed by | Purpose |
|---|---|---|
| `MOCK_OAUTH_PORT` | `mock-oauth` | listen port (default 9000) |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `MAIL_FROM` / `MAIL_TO` / `MAIL_BCC` / `MAIL_SUBJECT` / `MAIL_BODY` / `MAIL_ATTACHMENT_PATH` | `smtp-client` | see table above |
| `FAKE_UPSTREAM_PORT` | `fake-upstream` | listen port (default 587) |
| `FAKE_UPSTREAM_CERT_FILE` / `FAKE_UPSTREAM_KEY_FILE` | `fake-upstream` | paths to the mounted cert/key |

Every other variable this feature sets (`OAUTH_*`, `RELAY_*`, `REVIEWER_EMAILS`, `SMTP_USERNAME`/`SMTP_PASSWORD` on the app side, `JWT_SECRET_KEY`, `FRONTEND_URL`) is **existing** application configuration (features 001/002) supplied via Compose `environment:` — this feature introduces no new variables on the application side.
