# Presigned URLs

A presigned URL lets someone upload or download a single object without ever holding the bucket's
credentials - the authorization is embedded in the URL itself, valid until it expires.

## Getting one

```
POST /buckets/{bucket}/presign
Authorization: S3BENDER-HMAC-SHA256 AccessKey=...,Timestamp=...,Signature=...
Content-Type: application/json

{ "key": "docs/report.pdf", "method": "GET", "expiresInSeconds": 3600 }
```

`method` is `GET` (download) or `PUT` (upload). `expiresInSeconds` is capped at
`S3BENDER_MAX_PRESIGN_EXPIRY_SECONDS` (default 604800 = 7 days).

Response:

```json
{
  "url": "https://files.example.com/buckets/demo/objects/docs/report.pdf?AccessKey=AK...&Expires=1787600000&Signature=...",
  "method": "GET",
  "expiresAt": "2026-08-24T18:20:00Z"
}
```

## Using one

Just `curl`/`fetch`/browser-navigate the URL directly - no headers required:

```bash
curl "$PRESIGNED_URL"                       # for a GET presign
curl -X PUT --data-binary @file.pdf "$PRESIGNED_URL"   # for a PUT presign
```

The method in the request must match the method the URL was signed for; sending a `PUT` to a
`GET`-presigned URL (or vice versa) fails signature verification, because the method is part of
the signed string.

## Properties

- **Single-use restriction: none.** A presigned URL is valid for every matching request until it
  expires, not just the first one - the same as S3's presigned URLs. If you need one-time-use
  semantics, layer that on the application side (e.g. delete/rotate on first use).
- **Not revocable individually.** The only way to invalidate a presigned URL early is to rotate
  the bucket's secret key (which also invalidates every other outstanding presigned URL and header
  credential for that bucket - there is currently no per-URL revocation list).
- **Bound to one bucket, one key, one method.** A presigned URL for `GET /a.txt` in bucket `demo`
  cannot be reused for `b.txt`, for `PUT`, or for any other bucket.
- **`S3BENDER_PUBLIC_BASE_URL`** overrides the scheme/host/port used to build the URL, for
  deployments behind a reverse proxy where the server's view of its own address doesn't match the
  public one. Set it to e.g. `https://files.example.com`.

See [auth-and-signing.md](auth-and-signing.md) for the exact signature computation.
