#!/usr/bin/env python3
"""Non-interactive SMTP test client for the mockmt local e2e rig.

Driven entirely by environment variables (see
specs/003-e2e-docker-testrig/data-model.md's configuration table). Sends
exactly one message and exits -- no prompts, no interactive state,
runnable via `docker compose run --rm smtp-client` with `-e` overrides
per invocation.

Bcc handling is deliberate, not incidental: the message object below
never has a Bcc header set on it at all. Any hidden recipient reaches the
server purely through the low-level sendmail() envelope recipient list,
exactly matching how a real MTA handles Bcc. This makes the relay
feature's hidden-recipient check (FR-011) a faithful envelope-vs-header
test rather than an accident of some library silently stripping a header
for an unrelated reason (research R15).
"""
import os
import smtplib
from email.message import EmailMessage


def env(name, default):
    return os.environ.get(name, default)


def main():
    host = env("SMTP_HOST", "app")
    port = int(env("SMTP_PORT", "1025"))
    username = env("SMTP_USERNAME", "agent")
    password = env("SMTP_PASSWORD", "agent-pass")
    mail_from = env("MAIL_FROM", "agent@example.com")
    mail_to = env("MAIL_TO", "test@example.com")
    mail_bcc = env("MAIL_BCC", "")
    subject = env("MAIL_SUBJECT", "Test message from the e2e rig")
    body = env("MAIL_BODY", "This is a test message sent by the e2e smtp-client.")
    attachment_path = env("MAIL_ATTACHMENT_PATH", "")

    msg = EmailMessage()
    msg["From"] = mail_from
    msg["To"] = mail_to
    msg["Subject"] = subject
    msg.set_content(body)

    if attachment_path:
        with open(attachment_path, "rb") as f:
            data = f.read()
        filename = os.path.basename(attachment_path)
        msg.add_attachment(data, maintype="application", subtype="octet-stream", filename=filename)

    to_addrs = [mail_to]
    if mail_bcc:
        to_addrs.append(mail_bcc)

    with smtplib.SMTP(host, port, timeout=15) as smtp:
        smtp.ehlo()
        smtp.login(username, password)
        smtp.sendmail(mail_from, to_addrs, msg.as_string())

    print(f"Sent message from {mail_from} to {to_addrs} via {host}:{port}")


if __name__ == "__main__":
    main()
