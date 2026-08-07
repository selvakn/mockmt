# Phase 0 Research: Local End-to-End Test Environment

**Feature**: `003-e2e-docker-testrig` | **Date**: 2026-08-07 | **Plan**: [plan.md](./plan.md)

Every claim below was verified against either the actual application source, the pinned dependency versions' source, or a live check in this environment (Docker was available, so several assumptions were confirmed empirically rather than asserted from memory) — the same standard applied throughout this project's earlier features.

---

## R1. Why the app's own SMTP server cannot double as the relay's stand-in destination

Confirmed by reading `internal/mockmt/smtp.go`'s `StartSMTPServer`: it sets `s.AllowInsecureAuth = true` and never sets `s.TLSConfig`, so it never advertises `STARTTLS` and cannot serve implicit TLS either. The relay client (`relay_sender.go`) requires one of the two TLS modes unconditionally (FR-029 from feature 002 — no plaintext relay option). A dedicated stand-in destination server is therefore structurally necessary; the app's own capture-only server cannot be pointed at itself.

## R2. Found while designing this: relay TLS hostname verification is silently skipped today

Verified against the Go standard library source (`GOROOT/src/crypto/x509/verify.go:589`): `x509.Certificate.Verify` only calls `VerifyHostname` when `opts.DNSName` (derived from `tls.Config.ServerName`) is non-empty. `relay_sender.go`'s `tlsClientConfigFor(cfg)` never sets `ServerName` on either branch (implicit TLS or STARTTLS), so today the certificate **chain** is verified (against `RootCAs`) but the certificate is never checked to actually belong to the host being dialed. Confirmed this is not rescued by `go-smtp` internally either: `Client.startTLS`'s own `ServerName`-filling logic only fires when `c.serverName` was set by the package-level `Dial`/`DialTLS`/`DialStartTLS` helpers, and the app's code builds its own `net.Conn` via `net.DialTimeout` and passes it to `NewClientStartTLS`, so `c.serverName` is always empty.

**Decision**: fix `tlsClientConfigFor` to set `ServerName: cfg.Host` whenever the resolved config doesn't already have one, cloning rather than mutating (the config is a long-lived, concurrently-used field on `relayCfg`; `RelayMaxConcurrentIO` defaults to 2, so concurrent `relaySend` calls are an expected, not edge, case). This is a prerequisite for FR-006/SC-005 (the relay verification must exercise genuine trust-checking) and is bundled into this feature rather than filed separately, since this feature is what surfaced it and needs it to be true for its own success criteria to mean anything.

**Confirmed safe against the existing suite**: every test that reaches the TLS handshake (`relay_sender_test.go`) supplies its own `cfg.TLSConfig` with `ServerName` already set to `"127.0.0.1"` (via `generateSelfSignedCert` in `relay_testsupport_test.go`), so the `if ServerName == ""` guard leaves those untouched. `TestRelaySendDialFailureIsConfirmed` fails before the TLS branch is ever reached. No test targets `tlsClientConfigFor` directly today; one is added.

## R3. Why two app instances, not one switched between modes

Per the spec's resolved scope, both capture-only and relay-with-approval need end-to-end coverage, and `RELAY_ENABLED` is a single process-wide flag read once at startup (`LoadRelayConfig`) — a running instance cannot change mode without a restart. Two named service instances of the *same* image, differing only in configuration, avoids any restart choreography and lets both be verified in one session. Confirmed the Dockerfile's baked `ENV PORT=8080`/`SMTP_PORT=1025` never need overriding for the second instance — only the **host** side of the port mapping differs; `FRONTEND_URL` and `OAUTH_REDIRECT_URI` do need per-instance overrides, since both are absolute URLs baked around whichever host port that instance is reachable on.

## R4. `godotenv` / environment precedence

Confirmed `main.go` calls `godotenv.Load()` unconditionally and `godotenv` never overrides a variable already present in the OS environment. The Dockerfile bakes `env.example` to `/app/.env` and sets several `ENV` instructions directly (`PORT`, `SMTP_PORT`, `FRONTEND_URL`, `SERVE_FRONTEND_DIST`, `GIN_MODE`) — both of these are real OS environment values by the time the process starts, and Compose's `environment:` block is exactly the same mechanism, set before the container's entrypoint runs. So every variable this feature needs to set or override via Compose reliably wins, with no risk of the baked `.env` file interfering.

## R5. Why the mock OAuth server needs almost no logic, verified against the app's actual flow

Read `internal/mockmt/auth.go`, `frontend/src/views/Login.vue`, `AuthCallback.vue`, `frontend/src/stores/auth.js`:

- `handleOAuthLogin` calls `oauthConfig.AuthCodeURL("state")` — the **literal string** `"state"`, not a per-session random value, and `handleOAuthCallback` never reads or validates `state` at all. The mock's `/authorize` handler can echo it back unchecked; there is nothing to get wrong here.
- `Login.vue`'s "Sign in" is a full-page `window.location.href = '/auth/oauth'` (same-origin, relative), not an XHR — so it correctly hits whichever app instance's own origin the developer is currently on. The mock OAuth server never needs to know which app instance initiated a given flow; `redirect_uri` in the incoming request carries that.
- `handleOAuthCallback` does `oauthConfig.Exchange(...)` then `getUserInfoFromOAuth(token.AccessToken)`, expecting a JSON body with at least `email`/`name` (`OAuthUserInfo` struct: `sub`, `email`, `email_verified`, `name`, `picture`, `preferred_username`).
- `AuthCallback.vue` reads only `?token=` from the query string and stores it under the literal `localStorage` key `"token"`.

## R6. Critical: the mock `/token` endpoint's `Content-Type` header

Verified in `golang.org/x/oauth2@v0.36.0/internal/token.go` (`doTokenRoundTrip`): the response is parsed as `application/x-www-form-urlencoded`/`text/plain` in one branch, and as JSON in a `default` branch for everything else. Go's `net/http` **content-sniffs** the first `Write()` when no `Content-Type` has been explicitly set, and a body starting with `{` is typically sniffed as `text/plain; charset=utf-8` — which routes into the form-encoded branch, `url.ParseQuery` on a JSON blob yields nothing, and `Exchange()` fails with "server response missing access_token" on **every single login attempt**. The mock handler must call `w.Header().Set("Content-Type", "application/json")` before writing the body. This is the single most important correctness detail in the mock server, confirmed by reading the actual client library rather than assumed.

## R7. A real, separate, pre-existing robustness bug the mock must not trigger

`auth.go`'s `getUserInfoFromOAuth` does an unconditional `accessToken[:8]` for a log line. `gin.Default()` wires in `Recovery()`, so a short token doesn't crash the process, but it does surface as an opaque recovered-panic 500 on login — far more confusing to debug in an e2e run than a clean failure. **Decision**: the mock issues access tokens as 32 hex characters (`crypto/rand`), comfortably clear of the panic. This is a pre-existing bug in shipped code, out of scope to fix here, but the mock is designed to never trip over it.

## R8. `RELAY_CA_CERT_FILE` expects a bare CA certificate, not a cert+key pair

Confirmed via `internal/mockmt/relay_config.go`'s `loadRelayTLSConfig`: it only ever does `os.ReadFile` + `pool.AppendCertsFromPEM`, building `&tls.Config{RootCAs: pool}` — there is no `tls.Certificate`/client-certificate loading anywhere in that path. The stand-in destination server's **public certificate** is what the relay-mode app instance mounts via this variable; its private key is mounted only into the stand-in server itself. The two are never confused, and neither needs to be baked into any Docker image — both are runtime bind mounts, generated once and checked in as static, non-secret, test-only files (self-signed, 10-year validity, SAN covering the Compose service's internal DNS name).

## R9. Bonus finding: `DB_PATH` is dead configuration (informs, doesn't require, the "always ephemeral" design)

`internal/mockmt/database.go` reads `getEnv("DATABASE_URL", "./webmail.db")` — it never reads `DB_PATH`, which the Dockerfile nonetheless sets (`ENV DB_PATH=/app/data/webmail.db`). The actual runtime default (`./webmail.db` relative to `WORKDIR /app`) writes to the container's own ephemeral filesystem regardless of that unused variable. This confirms the "clean slate on every startup" requirement (FR-009) holds with **zero** Compose configuration — no volume to deliberately omit, no `DATABASE_URL` override needed. (This also means the *existing*, unrelated production `docker-compose.yml`'s `./data:/app/data` mount doesn't actually persist anything today unless an operator's own `.env` sets `DATABASE_URL` accordingly — a pre-existing gap in that file, not something this feature touches or should fix.)

## R10. Python's `smtplib` correctly falls back to `AUTH PLAIN`

Verified directly against the interpreter available in this environment (`smtplib.py`'s `login()`): it builds `advertised_authlist` from whatever the server's `EHLO` response actually lists, then filters its own preferred order (`['CRAM-MD5', 'PLAIN', 'LOGIN']` or `['PLAIN', 'LOGIN']`) down to the intersection. Since `Session.AuthMechanisms()` in `smtp.go` returns exactly `[]string{sasl.Plain}`, the server's `EHLO` advertises only `AUTH PLAIN`, and the filtered list is `['PLAIN']` regardless of preference order. No special handling is needed in the test client.

## R11. Alpine's BusyBox provides both healthcheck primitives needed, confirmed by running the image

Ran `alpine:3.20` locally: `wget` and `nc` are both present at `/usr/bin/{wget,nc}`, provided by the same BusyBox multi-call binary. This is sufficient for both healthchecks this feature needs — `wget --spider` against the mock OAuth server's HTTP endpoint, and `nc -z` for the stand-in destination server's raw SMTP port — without adding `curl` or any other package, and without needing a shell-capable base for the main application images (which are untouched).

## R12. Module boundary: one `e2e/go.mod`, not two

A single `e2e/go.mod` covering both new Go programs (the mock OAuth server, stdlib-only, and the stand-in destination server, which needs `github.com/emersion/go-smtp` and `github.com/emersion/go-sasl` — the same libraries and versions already vetted and pinned by the main module) is simpler to maintain (one `go.sum`, one `gofmt`/`go vet` target) than splitting them. The cost — the OAuth server's build layer resolves modules it doesn't import — is negligible for a two-binary, test-only module. Neither program is referenced by, or referenced from, the main module's `go.mod`; `go build ./...` from the repo root does not cross into `e2e/` at all, since it is a separate module root.

## R13. Docker build context: `e2e/*` Dockerfiles must use `context: ./e2e`

Since `go.mod` lives at `e2e/go.mod`, both new Dockerfiles need `context: ./e2e` with `dockerfile: <program>/Dockerfile` (paths relative to that context) — a context scoped to `./e2e/mockoauth` alone would not contain `go.mod` and would fail. Separately, `app`/`app-relay` continue to build with `context: .` (repo root, matching the existing production `docker-compose.yml`) — which means the root `.dockerignore` needs an `e2e` entry once that directory exists, or it silently bloats every root-context build (including the pre-existing, unrelated production compose file) without being referenced by either Dockerfile's `COPY` instructions.

## R14. Readiness signaling (resolved by clarification: startup must block until genuinely ready)

Neither `app` nor `app-relay` dials the mock OAuth server or the stand-in destination server at process startup — `initAuth()` only builds a config struct, and nothing is dialed until a user actually signs in or a reviewer presses Send Now. So correctness never strictly *required* startup ordering. The clarification session resolved this anyway: the single startup command must not report completion until every component is genuinely usable. **Decision**: `healthcheck` on both new services (`wget --spider` / `nc -z`, per R11) plus `depends_on: <service>: condition: service_healthy` on `app` (mock OAuth only) and `app-relay` (both). Compose's own `up` blocks on health-gated dependencies before returning, which is exactly the semantics FR-001 now requires — no custom polling script needed. `app`/`app-relay` themselves are not given healthchecks: the existing production Dockerfile's final stage is `distroless/base-debian12` with no shell, so a `CMD`-style healthcheck isn't available without modifying that Dockerfile, which this feature does not touch. A Gin server on a distroless image starts in well under a second in practice; the dev-browser verification pass tolerates this via ordinary page-load waiting rather than a formal health gate.

## R15. Bcc faithfulness in the test client

Confirmed `hiddenRecipients` (`internal/mockmt/relay_message.go`) classifies an envelope recipient as hidden purely by *absence* from the parsed `To`/`Cc` headers — it has no special awareness of a `Bcc:` header at all. **Decision**: the test client must never let a `Bcc:` header reach the wire (build the message with only a `To` header, then pass the additional address via the low-level `sendmail(from_addr, to_addrs, msg.as_string())` recipient list directly) so the hidden-recipient test is a faithful envelope-vs-header check, not an accident of some library silently stripping a header for an unrelated reason.
