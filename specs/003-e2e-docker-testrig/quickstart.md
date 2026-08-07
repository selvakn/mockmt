# Quickstart: Local End-to-End Test Environment

**Feature**: `003-e2e-docker-testrig` | **Date**: 2026-08-07

## Bring it up

```bash
docker compose -f docker-compose.e2e.yml up -d --build
```

This does not return until every component — the mock identity provider, the stand-in destination server, and both application instances — is genuinely ready (FR-001). No manual waiting, no retry loop.

## What's running

| Service | Reachable at | Purpose |
|---|---|---|
| `app` | http://localhost:8080 | capture-only mode |
| `app-relay` | http://localhost:8081 | relay-with-approval mode |
| `mock-oauth` | http://localhost:9000 | stand-in identity provider (browser hits this directly during login) |
| `fake-upstream` | internal only | stand-in destination server for `app-relay`'s relayed mail |

No real OAuth account, no real mail provider, no configuration beyond running the command above.

## Verify capture-only mode

1. Open http://localhost:8080, click **Sign in with OAuth**.
2. On the mock login page, type any address (e.g. `test@example.com`) and submit.
3. Land on the Dashboard, authenticated.
4. Send a test message:
   ```bash
   docker compose -f docker-compose.e2e.yml run --rm smtp-client
   ```
   (defaults target `app:1025`, `MAIL_TO=test@example.com` — match whatever address you signed in as).
5. Refresh the Dashboard — the message appears; open it and confirm subject/body render.

## Verify relay-with-approval mode

1. Open http://localhost:8081, sign in via the mock as `reviewer@example.com` (this address is in `app-relay`'s `REVIEWER_EMAILS` — anything else won't see a queue).
2. Send a test message with a hidden recipient:
   ```bash
   docker compose -f docker-compose.e2e.yml run --rm \
     -e SMTP_HOST=app-relay -e MAIL_TO=customer@example.com -e MAIL_BCC=audit@example.com \
     smtp-client
   ```
3. Open the Review Queue — the message is there. Open it: `audit@example.com` is listed and flagged as hidden from the other recipients.
4. Press **Send Now**. The portal reports it sent.
5. Confirm independently — this is the point of the whole exercise:
   ```bash
   docker compose -f docker-compose.e2e.yml logs fake-upstream
   ```
   A line confirming receipt, matching the sender/recipients/subject just sent.

## Tear down

```bash
docker compose -f docker-compose.e2e.yml down
```

Every subsequent `up` starts from empty — no message, queue item, or review history survives a restart (FR-009). This is a separate file from the existing `docker-compose.yml`; nothing about a real deployment is touched by either command.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `up` hangs | A health-gated dependency never became ready — check `docker compose -f docker-compose.e2e.yml ps` for which service is unhealthy |
| Login fails with a 500 from `/auth/callback` | Almost certainly the mock's `/token` response is missing `Content-Type: application/json` (research R6) — check `docker compose logs mock-oauth` |
| Review Queue is empty / access denied | Signed in as an address not in `REVIEWER_EMAILS` (`reviewer@example.com`) |
| Send Now fails with a certificate error | The stand-in server's SAN must match `RELAY_HOST` exactly (`fake-upstream`) — this is now enforced for real (research R2) |
| Port already in use | Something else on the host already bound 8080/8081/9000/1025/1026 — stop it or adjust the host-side mapping in `docker-compose.e2e.yml` |
