# Contract: Mock OAuth Server

**Feature**: `003-e2e-docker-testrig` | **Date**: 2026-08-07

Defines the exact HTTP behavior `e2e/mockoauth` must implement, derived entirely from how the application actually calls an OAuth provider (`internal/mockmt/auth.go`, `golang.org/x/oauth2`) — not from any real provider's spec. This is a disposable double: it implements exactly enough of OAuth2's authorization-code flow for the app to complete a login, nothing more.

## `GET /authorize`

Query parameters the app sends (via `oauthConfig.AuthCodeURL("state")`): `client_id`, `redirect_uri`, `response_type=code`, `scope`, `state` (always the literal string `"state"` — never validated by the app, echoed back unchecked here).

Response `200`: a minimal HTML form — one text input (`email`, default value `test@example.com`, freely editable) and a submit button posting back to this same path with the original query parameters preserved as hidden fields.

## `POST /authorize` (form submission from the page above)

Body: `email` plus the preserved `client_id`/`redirect_uri`/`state`.

Behavior:
1. Generate a random opaque code (`crypto/rand`, hex-encoded, unpredictable).
2. Store `code → {email, name: email, redirect_uri}` in memory, single-use.
3. `302` to `<redirect_uri>?code=<code>&state=<state>`.

No `client_id`/`redirect_uri` validation against a registry — accept whatever was sent.

## `POST /token`

Standard `application/x-www-form-urlencoded` body (`golang.org/x/oauth2`'s client sends `grant_type=authorization_code&code=...&redirect_uri=...&client_id=...&client_secret=...`).

Behavior:
1. Look up `code`. If absent or already redeemed, respond `400` with `{"error": "invalid_grant"}` (also form-urlencoded-safe, though this path is not expected to be exercised in normal use).
2. On success: generate a 32-hex-character access token (`crypto/rand`) — **must** be comfortably longer than 8 characters (research R7). Store `access_token → {email, name}`, delete the code (single-use).
3. Respond `200`, **`Content-Type: application/json`, set explicitly before writing** (research R6 — this is the one detail that breaks every login if missed):
   ```json
   { "access_token": "<32 hex chars>", "token_type": "Bearer", "expires_in": 3600 }
   ```

## `GET /userinfo`

Header: `Authorization: Bearer <access_token>`.

Behavior: look up the token; if unknown, `401`. On success, `200` with `Content-Type: application/json`:
```json
{ "sub": "<same as email>", "email": "<email>", "email_verified": true, "name": "<name>", "picture": "" }
```
Field names match `OAuthUserInfo` in `auth.go` exactly (`sub`, `email`, `email_verified`, `name`, `picture`, `preferred_username` — the last is omitted since `name` is always present here and `auth.go` only falls back to `preferred_username` when `name` is empty).

## Healthcheck

`GET /authorize` with no query parameters returns `200` with the login form (missing parameters are not validated) — sufficient for `wget --spider` (research R11); no dedicated `/healthz` endpoint needed.

## Explicitly not implemented

- Any `client_id`/`client_secret` validation.
- `state` validation (the app itself never checks it).
- Token expiry enforcement, refresh tokens, PKCE, or any OIDC discovery document.
- Persistence of any kind — a server restart invalidates all outstanding codes/tokens, which is fine since nothing in this environment is expected to survive a restart anyway (FR-009).
