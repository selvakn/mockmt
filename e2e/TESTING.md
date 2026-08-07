# Running the e2e test rig (repeatable runbook)

This is the exact, repeatable procedure for exercising the whole app —
both capture-only and relay-with-approval modes — against the Docker
Compose rig in [`docker-compose.e2e.yml`](../docker-compose.e2e.yml). It
mirrors the acceptance checks in
[`specs/003-e2e-docker-testrig/tasks.md`](../specs/003-e2e-docker-testrig/tasks.md)
(Phases 3–5) and can be re-run end to end whenever the rig, the relay
code, or the OAuth/SMTP wiring changes.

For narrative background (what each service is and why it exists), see
[`specs/003-e2e-docker-testrig/quickstart.md`](../specs/003-e2e-docker-testrig/quickstart.md).
This document is the "just run it again" version.

## Prerequisites

- Docker + Docker Compose v2 (`docker compose`, not `docker-compose`)
- The [`dev-browser`](https://www.npmjs.com/package/dev-browser) CLI, for
  the browser-driven steps (`npm install -g dev-browser && dev-browser install`)
- Run everything from the repo root

## 1. Build and bring up the stack

```sh
docker compose -f docker-compose.e2e.yml up -d --build
```

This blocks until `mock-oauth` and `fake-upstream` report healthy — it
will not return early into a not-yet-ready stack.

**Expected**: `docker compose -f docker-compose.e2e.yml ps` shows four
services (`app`, `app-relay`, `mock-oauth`, `fake-upstream`) all `Up`,
with `mock-oauth`/`fake-upstream` additionally `(healthy)`. No
restart loops. No port-binding errors on 8080/8081/9000/1025/1026.

```sh
docker compose -f docker-compose.e2e.yml ps
```

Sanity-check the mock IdP standalone before layering a login flow on
top of it:

```sh
curl -s http://localhost:9000/authorize | head -20
```

**Expected**: renders the `Mock OAuth Login` HTML form.

## 2. Verify capture-only mode (`app`, :8080)

### 2a. Sign in

```sh
dev-browser <<'EOF'
const page = await browser.getPage("app");
await page.goto("http://localhost:8080");
await page.click('button:has-text("Sign in with OAuth")');
await page.waitForLoadState("networkidle");
await page.fill('input[name="email"]', 'test@example.com');
await page.click('button:has-text("Log in")');
await page.waitForLoadState("networkidle");
console.log("URL:", page.url());
console.log((await page.snapshotForAI()).full);
EOF
```

**Expected**: lands back on `http://localhost:8080/`, header shows
`test@example.com`, "0 emails".

### 2b. Send a message into it

```sh
docker compose -f docker-compose.e2e.yml run --rm smtp-client
```

Defaults: `SMTP_HOST=app`, `MAIL_TO=test@example.com`. Override any of
the variables in [`data-model.md`](../specs/003-e2e-docker-testrig/data-model.md)'s
configuration table with `-e` as needed.

**Expected**: `Sent message from agent@example.com to ['test@example.com'] via app:1025`

### 2c. Confirm it renders

```sh
dev-browser <<'EOF'
const page = await browser.getPage("app");
await page.reload();
await page.waitForLoadState("networkidle");
console.log((await page.snapshotForAI()).full);
EOF
```

**Expected**: header now shows "1 emails"; the message with subject
`Test message from the e2e rig` is listed. Click it (or add
`await page.click('text=Test message from the e2e rig')` to the script
above) and confirm the From address and body text render.

### 2d. Confirm per-recipient scoping

Sign out (click the account button, **Sign out**) and sign back in as a
different address (e.g. `other@example.com`) in the same script pattern
as 2a. **Expected**: "0 emails" / "No emails yet" — the previous
message must not be visible to a different identity.

## 3. Verify relay-with-approval mode (`app-relay`, :8081)

### 3a. Sign in as the reviewer

```sh
dev-browser <<'EOF'
const page = await browser.getPage("relay");
await page.goto("http://localhost:8081");
await page.click('button:has-text("Sign in with OAuth")');
await page.waitForLoadState("networkidle");
await page.fill('input[name="email"]', 'reviewer@example.com');
await page.click('button:has-text("Log in")');
await page.waitForLoadState("networkidle");
console.log((await page.snapshotForAI()).full);
EOF
```

**Expected**: header shows a `"Relay mode: ON"` badge and a
**Review Queue** link — proof `REVIEWER_EMAILS=reviewer@example.com` is
wired correctly. Any other address would not see either.

### 3b. Send a message with a hidden (Bcc) recipient

```sh
docker compose -f docker-compose.e2e.yml run --rm \
  -e SMTP_HOST=app-relay -e MAIL_TO=customer@example.com -e MAIL_BCC=audit@example.com \
  smtp-client
```

**Expected**: `Sent message from agent@example.com to ['customer@example.com', 'audit@example.com'] via app-relay:1025`

### 3c. Confirm the hidden recipient is flagged, and open it

```sh
dev-browser <<'EOF'
const page = await browser.getPage("relay");
await page.click('text=Review Queue');
await page.waitForTimeout(800);
await page.click('text=Test message from the e2e rig');
await page.waitForLoadState("networkidle");
console.log((await page.snapshotForAI({ depth: 30 })).full);
EOF
```

**Expected**: the message is `pending_review`. The Recipients section
lists `customer@example.com` and `audit@example.com`, with
`audit@example.com` annotated `hidden from other recipients`. The
message body renders under "Message (text)".

### 3d. Approve it

```sh
dev-browser <<'EOF'
const page = await browser.getPage("relay");
await page.click('button:has-text("Send Now")');
await page.waitForTimeout(1500);
console.log((await page.snapshotForAI()).full);
EOF
```

**Expected**: the message drops out of the `Pending Review` filter.
Switch the filter dropdown to `Sent` to confirm it's there with state
`sent`.

### 3e. Confirm delivery independently

This is the point of the whole rig — don't trust the portal's own
report:

```sh
docker compose -f docker-compose.e2e.yml logs fake-upstream
```

**Expected**: a line like

```
received message: from=relay@example.com to=[customer@example.com audit@example.com] subject="Test message from the e2e rig" size=270 bytes
```

matching the sender identity (`RELAY_IDENTITY`), both recipients
(including the hidden one), and the subject just sent. (Repeated
`connection reset by peer` lines from `fake-upstream` are just the
`nc -z` healthcheck probing the port — harmless, not a delivery error.)

## 4. Tear down

```sh
docker compose -f docker-compose.e2e.yml down
```

**Expected**: `docker ps -a --filter name=mockmt-` and
`docker network ls --filter name=mockmt` both come back empty. No
volumes are declared, so there is nothing to prune — the next `up`
always starts from a clean database in both app instances.

## 5. Full regression check (no browser required)

Whenever the bundled fix in `internal/mockmt/relay_sender.go`
(`tlsClientConfigFor`) or anything else in the main module changes, run
before repeating the steps above:

```sh
go build ./... && go vet ./... && go test ./... -race
make ci                      # fmt-check, vet, lint, test
cd e2e && go build ./... && go vet ./...   # e2e module compiles independently
```

## Quick command reference

| Step | Command |
|---|---|
| Build | `docker compose -f docker-compose.e2e.yml build` |
| Up (blocks until ready) | `docker compose -f docker-compose.e2e.yml up -d --build` |
| Status | `docker compose -f docker-compose.e2e.yml ps` |
| Send to capture-only | `docker compose -f docker-compose.e2e.yml run --rm smtp-client` |
| Send to relay (with Bcc) | `docker compose -f docker-compose.e2e.yml run --rm -e SMTP_HOST=app-relay -e MAIL_TO=customer@example.com -e MAIL_BCC=audit@example.com smtp-client` |
| Independent delivery proof | `docker compose -f docker-compose.e2e.yml logs fake-upstream` |
| Down | `docker compose -f docker-compose.e2e.yml down` |
