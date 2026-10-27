# Access Token Generation - User and Service Accounts

_Revamped API. Transcribed from [access-token-generation.pdf](access-token-generation.pdf) (received 2026-10-27). The PDF is the original; this copy exists so the text can be searched and diffed._

## What is an access token?

An access token is a short-lived key that proves who is calling the API. You get one from `POST /v1/oauth2/token` and send it on every other API call.

- It identifies the caller (a user or a service account) and the Lucidity account it belongs to. What it can do is set by that caller's roles.
- It expires after 15 minutes. After that, request a new one with the same refresh token or client credentials.
- Keep it secret, like a password. Don't log it or commit it to code.

## Getting an access token

Users and service accounts get an access token from the same endpoint. Only the request body differs. This call needs no Authorization header.

| Method | Path | Content-Type |
| --- | --- | --- |
| POST | /v1/oauth2/token | application/json |

### Request: user

Send the refresh token you generated in the dashboard.

Request body:

```json
{
  "refreshToken": "<accessKey>.<secretKey>"
}
```

curl:

```bash
curl -X POST "https://dash-back.lucidity.dev/v1/oauth2/token" \
  -H "Content-Type: application/json" \
  -d '{
    "refreshToken": "<accessKey>.<secretKey>"
  }'
```

### Request: service account

Send the clientId and clientSecret returned when the credential was created with `POST /v1/service-accounts/{id}/tokens`. The secret is shown only once, at creation.

Request body:

```json
{
  "clientId": "<clientId>",
  "clientSecret": "<clientSecret>"
}
```

curl:

```bash
curl -X POST "https://dash-back.lucidity.dev/v1/oauth2/token" \
  -H "Content-Type: application/json" \
  -d '{
    "clientId": "<clientId>",
    "clientSecret": "<clientSecret>"
  }'
```

Send one shape or the other, never both.

### Response: 200 OK

```json
{
  "success": true,
  "data": {
    "accessToken": "eyJhbGciOiJSUzI1NiIs..."
  },
  "requestId": "0c8e3f5b9a2d4c71b6e0f14d2a9c7e53"
}
```

### Using the token

Send it as a Bearer token on every API call:

```bash
curl "https://dash-back.lucidity.dev/v1/service-accounts" \
  -H "Authorization: Bearer <accessToken>"
```

When the token expires (after 15 minutes), call `POST /v1/oauth2/token` again with the same body. There is no refresh token.

## Errors

Errors use this shape:

```json
{
  "success": false,
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "provide either refreshToken, or clientId and clientSecret, not both"
  },
  "requestId": "dbc3a23c1d64444085cc57bea27552fc"
}
```

Branch on `error.code`. `error.message` is for people and may change.

| Status | error.code | When |
| --- | --- | --- |
| 400 | VALIDATION_ERROR | The body has no credentials, has clientId without clientSecret, or has both shapes. |
| 400 | INVALID_REQUEST | The credentials aren't in the expected format (for example, a refresh token without the "."). |
| 401 | UNAUTHORIZED | One of: wrong ID or secret, a revoked or expired credential, or a disabled or deleted service account. |
| 500 | INTERNAL_ERROR | Server-side failure. Quote the requestId when you report it. |

## Replaced endpoints

These endpoints no longer exist. Use `POST /v1/oauth2/token` instead.

| Old endpoint | Was used by |
| --- | --- |
| POST /public/v1/auth/tokens | Users |
| POST /external/client/api/v1/auth/token (form body + Basic auth) | Service accounts |
