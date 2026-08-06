# Quickstart: SMTP Authentication

## Configure credentials

Set two environment variables before starting the server (locally or in `.env`, following the existing `env.example` pattern):

```
SMTP_USERNAME=devbox
SMTP_PASSWORD=s3cr3t
```

Both are required. If either is missing or empty, the server logs a fatal error and exits without starting the SMTP listener.

## Run the server

```
go run .
```

## Send an authenticated test email

Using the updated `cmd/test_email` helper (after this feature updates it to prompt for/accept credentials):

```
go run ./cmd/test_email
```

Or with any SMTP client library that supports `AUTH PLAIN`, e.g. Go's standard library:

```go
auth := smtp.PlainAuth("", "devbox", "s3cr3t", "localhost")
err := smtp.SendMail("localhost:1025", auth, "test@example.com", []string{"user@localhost"}, message)
```

## Verify rejection of unauthenticated mail

Attempt to send without credentials (e.g., `smtp.SendMail(..., nil, ...)` or `telnet localhost 1025` followed directly by `MAIL FROM:<test@example.com>` without an `AUTH` step first) — the server must respond `502 5.7.0 Please authenticate first` and the message must not appear in the recipient's inbox.

## Verify rejection of bad credentials

Authenticate with an incorrect username or password — the server must respond `535 5.7.8 Authentication failed`.

## Docker

Update the `docker run` invocation (README) to include the two new variables, e.g.:

```
docker run --rm \
    -v ./data:/data \
    -e DATABASE_URL=/data/mockmt.db \
    -e SMTP_PORT=2525 \
    -e SMTP_USERNAME=devbox \
    -e SMTP_PASSWORD=s3cr3t \
    -e PORT=8080 \
    -e JWT_SECRET_KEY=s3cr3t \
    ...
    ghcr.io/selvakn/mockmt:latest
```
