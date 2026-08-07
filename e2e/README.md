# e2e — local Docker test rig for mockmt

A disposable Docker Compose stack that stands in for a real OAuth identity
provider and a real upstream mail server, so the whole app — both
capture-only and relay-with-approval modes — can be exercised end to end
on a laptop before shipping. See
[`specs/003-e2e-docker-testrig/quickstart.md`](../specs/003-e2e-docker-testrig/quickstart.md)
for the full walkthrough, contracts, and troubleshooting table, and
[`TESTING.md`](./TESTING.md) for the exact, repeatable step-by-step
runbook (including the `dev-browser` scripts) used to verify both
modes end to end.

This directory is its own Go module (`module e2e`), kept completely
separate from the shipped application's `go.mod`/`go.sum` — nothing here
is a dependency of `mockmt` itself.

## Bring it up

```sh
docker compose -f docker-compose.e2e.yml up -d --build
```

Blocks until `mock-oauth` and `fake-upstream` report healthy. Then:

- Capture-only app: http://localhost:8080
- Relay-with-approval app: http://localhost:8081 (reviewer identity must be
  `reviewer@example.com`, matching `REVIEWER_EMAILS` in the compose file)
- Mock OAuth login page: http://localhost:9000/authorize

Send a test message (defaults target `app:1025`, addressed to
`test@example.com`):

```sh
docker compose -f docker-compose.e2e.yml run --rm smtp-client
```

Send one into the relay instance with a hidden (Bcc) recipient:

```sh
docker compose -f docker-compose.e2e.yml run --rm \
  -e SMTP_HOST=app-relay -e MAIL_TO=customer@example.com -e MAIL_BCC=audit@example.com \
  smtp-client
```

## Tear it down

```sh
docker compose -f docker-compose.e2e.yml down
```

No volumes are declared — every database is ephemeral per-container, so
each `up` starts from a clean slate.
