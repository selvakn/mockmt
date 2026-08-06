# Quickstart: SMTP Relay with Human Approval

**Feature**: `002-smtp-relay-approval` | **Date**: 2026-08-06

How to run, configure, and exercise the feature once implemented.

---

## Nothing changes if you do nothing

Relay is off by default. An existing deployment that upgrades and touches no configuration keeps behaving exactly as before: mail is captured, shown in the portal, and never leaves the machine (FR-002, SC-001).

Everything below applies only once you deliberately switch relay on.

---

## 1. Get an upstream account

Using Gmail, which is the reference case:

1. Enable **2-Step Verification** on the Google account.
2. Create an **App Password** (Google Account → Security → App passwords). Use the 16-character value as `RELAY_PASSWORD`.

Your ordinary account password **will not work** — Google refuses basic authentication for SMTP. This is the most common setup failure.

Any provider speaking SMTP with STARTTLS or implicit TLS works the same way; only host and port differ.

---

## 2. Configure

Add to `.env` (see `env.example`):

```bash
# --- Relay mode -----------------------------------------------------
RELAY_ENABLED=true
RELAY_HOST=smtp.gmail.com
RELAY_PORT=587
RELAY_TLS_MODE=starttls              # starttls | tls  (no plaintext option)
RELAY_USERNAME=you@gmail.com
RELAY_PASSWORD=your_16_char_app_password
RELAY_IDENTITY=you@gmail.com         # replaces From on every relayed message
RELAY_TIMEOUT_SECONDS=10

# --- Who may approve ------------------------------------------------
REVIEWER_EMAILS=alice@example.com,bob@example.com

# --- Optional: private upstream CA ----------------------------------
# RELAY_CA_CERT_FILE=/etc/ssl/certs/internal-ca.pem

# --- Limits and memory ----------------------------------------------
MAX_MESSAGE_BYTES=26214400           # 25 MB, matching Gmail
SMTP_MAX_CONCURRENT=3                # inbound sessions; over-cap gets 421
RELAY_MAX_CONCURRENT_IO=2            # concurrent whole-message reads
SMTP_READ_TIMEOUT_SECONDS=60
SMTP_WRITE_TIMEOUT_SECONDS=60
RETENTION_DAYS=0                     # 0 = keep everything
```

### Sizing memory

The SQLite driver cannot stream blobs, so a whole message is held in memory while it is stored, relayed, or previewed. The ceiling is:

```text
MAX_MESSAGE_BYTES × (SMTP_MAX_CONCURRENT + RELAY_MAX_CONCURRENT_IO)
        25 MB     × (        3           +           2           )  ≈ 125 MB
```

Defaults are tuned for a 256 MB container, and `docker-compose.yml` sets `mem_limit: 256m` to match. If you raise any of the three, raise the container limit by the same arithmetic.

At these defaults three agents can submit at once and a fourth is told to retry; two reviewers can send or preview simultaneously and a third waits briefly. That is ample for a human-paced approval gate — but if you run a burst of agents, raise `SMTP_MAX_CONCURRENT` and the memory limit together.

There is intentionally no option to skip upstream certificate verification. For a self-hosted upstream with an internal certificate, point `RELAY_CA_CERT_FILE` at its CA.

For implicit TLS use `RELAY_PORT=465` and `RELAY_TLS_MODE=tls`.

`REVIEWER_EMAILS` must match the email your identity provider returns at login — that is what the reviewer check compares against, case-insensitively.

### Startup refuses bad configuration

With `RELAY_ENABLED=true`, the process exits rather than starting in a state where approvals would silently fail (FR-004, FR-017b). All missing settings are reported at once:

```text
relay mode is enabled but misconfigured:
  - RELAY_HOST is not set
  - RELAY_IDENTITY is not set
  - REVIEWER_EMAILS is empty (nobody could release queued mail)
```

An empty reviewer list is fatal on purpose: queueing mail nobody can release is worse than not accepting it.

---

## 3. Run

```bash
make run          # or: go run .
```

Startup log confirms the mode:

```text
Relay mode: ENABLED (upstream smtp.gmail.com:587, starttls, identity you@gmail.com)
Reviewers: 2 configured
Retention: disabled
Starting SMTP server at :25 (max 3 concurrent connections)
Starting web server on port 8080
```

Credentials never appear in the log (FR-006).

---

## 4. Send something as an agent would

Existing SMTP authentication still applies — submission requires `SMTP_USERNAME`/`SMTP_PASSWORD`:

```bash
go run ./cmd/test_email
```

Or from Python, as an agent would:

```python
import smtplib
from email.message import EmailMessage

msg = EmailMessage()
msg["From"] = "agent@myapp.local"
msg["To"] = "customer@example.com"
msg["Bcc"] = "audit@example.com"
msg["Subject"] = "Your quote"
msg.set_content("Hello, here is the quote you asked for.")

with smtplib.SMTP("localhost", 25) as s:
    s.login("your_smtp_username", "your_smtp_password")
    s.send_message(msg)
```

The client gets a normal `250`. Nothing has been delivered — the message is parked (FR-007).

---

## 5. Review and release

1. Log into the portal as an address in `REVIEWER_EMAILS`.
2. Open the review queue. Every queued message is listed regardless of recipient (FR-014).
3. Open the message. You see:
   - the sender the agent actually used, alongside the identity that will replace it (FR-013b);
   - the **envelope** recipient list — `audit@example.com` from the example appears here, flagged as hidden from the other recipients, even though it is in no visible header (FR-015a);
   - the text and HTML bodies, rendered in isolation (FR-016c);
   - attachments, previewable inline for images/PDF/text and downloadable otherwise (FR-016a/016b).
4. Press **Send Now**. The relay happens while you wait and the result comes back in the same interaction (FR-020a).

The recipient sees `RELAY_IDENTITY` as the sender, and replying reaches `agent@myapp.local` via `Reply-To` (FR-013a).

To stop a message instead, press **Reject**. It is never delivered, and the decision is recorded (FR-026).

---

## 6. When delivery fails

Failures are split into two kinds, and the difference matters (FR-024a):

**Confirmed** — the upstream explicitly refused, or the connection failed before anything was transmitted. Nothing was delivered. Retry freely.

```text
Failed — confirmed
authentication failed: SMTP error 535: Username and Password not accepted
```

**Indeterminate** — the message was fully transmitted but the acknowledgement never arrived. It may or may not have been delivered.

```text
Failed — possibly delivered
Timed out waiting for the upstream server to acknowledge the message. It may or may not have been delivered. (...)
```

Retrying an indeterminate message requires ticking a confirmation that a duplicate may reach the recipient (FR-025a). If it cannot be sent at all, **Abandon** moves it to Rejected and the audit notes the delivery status was unknown (FR-026a).

Killing the process mid-send is safe: on restart, anything left in `sending` is settled as Failed–indeterminate (FR-028).

---

## 7. Retention

Off by default. To bound disk growth once raw messages with attachments accumulate:

```bash
RETENTION_DAYS=90
```

After 90 days in a terminal state, a message's content and attachments are dropped. Its metadata and full audit trail are kept permanently, so "who released what, to whom, and when" survives (FR-034). Anything still pending review, or failed and unresolved, is never purged regardless of age (FR-035). A purged message says so in the portal and cannot be retried (FR-036).

---

## 8. Turning it back off

Set `RELAY_ENABLED=false` and restart. Pending messages stay stored in the database — nothing is deleted — but the review queue and every `/api/relay/*` endpoint disappear entirely (`404`), so there is no UI path to view or send them until relay is re-enabled. Previously captured mail is untouched (FR-018b).

---

## Troubleshooting

| Symptom | Cause |
|---|---|
| `535 Username and Password not accepted` | using the account password instead of an App Password, or 2-Step Verification is off |
| Startup exits listing missing settings | `RELAY_ENABLED=true` with an incomplete config — the listed variables are all of what is missing |
| Logged in but no queue visible | your address is not in `REVIEWER_EMAILS`, or does not match what the identity provider returns |
| `/api/relay/*` returns 404 | relay mode is disabled |
| Recipient sees the relay account, not the agent | expected — `From` is rewritten by design; replies still reach the agent via `Reply-To` |
| Message stuck in "Sending" | should be impossible; restart settles it as Failed–indeterminate |
| Timeouts on a healthy upstream | raise `RELAY_TIMEOUT_SECONDS`; the default of 10s is tuned to the reviewer's wait, not to slow networks |
| Agent gets `421 Too many concurrent connections` | more than `SMTP_MAX_CONCURRENT` (default 3) simultaneous submissions; it is a retryable 4xx, so raise the cap and the memory limit together if it is routine |
| Portal returns `503` with `Retry-After` on Send Now or preview | all `RELAY_MAX_CONCURRENT_IO` slots busy; retry, or raise the value and the memory limit together |
| Container OOM-killed under load | recompute the ceiling above; the three knobs and `mem_limit` must move together |
| `x509: certificate signed by unknown authority` | self-hosted upstream with an internal CA — set `RELAY_CA_CERT_FILE`. There is no skip-verify option, by design |
| Attachment preview shows 401 | something fetched the endpoint directly instead of via `fetchAsObjectUrl`; the bearer header is not sent on `src` subresource loads |

---

## Running the tests

```bash
make test         # or: go test ./...
```

Integration tests stand up a throwaway TLS SMTP server on loopback as the fake upstream, so no real provider or network access is needed. They cover partial recipient failure, the indeterminate path (upstream accepts the body then never replies), dial failure, and concurrent approvals of one message.
