# s3bender

A self-hosted, MinIO-like object storage server: buckets, objects, presigned URLs. One Go
binary (standard library HTTP + embedded SQLite, no CGO), Docker-first. The one deliberate difference from MinIO/S3: **every bucket
gets its own access key / secret key pair at creation time**, instead of one shared IAM identity
across all buckets - and unlike S3, that pair can be rotated on demand without touching the
bucket's objects.

Full design and API docs live in [_docs/](_docs/README.md) - read that first, especially
[_docs/auth-and-signing.md](_docs/auth-and-signing.md), before integrating a client. The docs are
written to be language-agnostic - nothing in them is specific to this implementation.

## Quick start

```bash
docker compose up -d --build         # builds locally; servers pull ghcr.io/king4eli/s3bender instead
curl http://localhost:9066/healthz   # API
curl http://localhost:9067/healthz   # same app, second port - this is what the console UI uses
```

Open http://localhost:9067, paste the admin key from `.env/engine.env`
(`S3BENDER_ADMIN_API_KEY`), create a bucket, and use the returned access/secret key to browse,
upload, download, presign, and rotate objects/credentials - all from the browser. Or drive the API
directly:

```bash
# create a bucket - get back a one-time secret key
curl -X POST http://localhost:9066/admin/buckets \
  -H "X-Admin-Api-Key: $S3BENDER_ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"demo"}'
```

Upload/download requires signing requests with the bucket's own key - see
[_docs/how-to-use.md](_docs/how-to-use.md) for a runnable end-to-end example (bash and
JavaScript, plus what to do if you lose a key), [_docs/auth-and-signing.md](_docs/auth-and-signing.md)
for the exact signing algorithm, or [_docs/presigned-urls.md](_docs/presigned-urls.md) for
generating a URL that needs no signing at the point of use.

## Repo layout

- `engine/` - the Go implementation: the whole app - bucket CRUD
  (create/list/delete/**rotate**), object PUT/GET/HEAD/DELETE/LIST, presign, HMAC auth, AES-256-GCM
  secret encryption, SQLite metadata + local-disk object storage.
  - `main.go` - reads config, starts both ports, background reindex, graceful shutdown.
  - `internal/server/` - `crypto.go` (AES-GCM + HMAC signing), `store.go` (SQLite),
    `storage.go` (bytes, sidecars, object index), `auth.go` (admin + bucket auth),
    `handlers.go`, `server.go` (routing). Tests sit beside them (`*_test.go`): signing vectors
    plus end-to-end flows against a real server (create→upload→download→presign, key rotation,
    public objects, pagination, reindex, Range/304 caching).
  - `wwwroot/` - the static console UI, embedded into the binary and served by this same process
    on a second port - see "How the console UI talks to the API" below.
- `_docs/` - the real documentation (architecture, data model, auth spec, API reference,
  how-to-use, deployment, porting guide) - protocol-level, not tied to this implementation.
- `docker-compose.yml` / `docker-compose.override.yml` - base (prod-shaped, pulls
  `ghcr.io/king4eli/s3bender`, host ports 9066/9067) + local dev override (adds `build:`).
- `.github/workflows/image.yml` - CI: Go tests, then a multi-arch image pushed to GHCR on every
  push to `main` and every `v*` tag. See `_docs/deployment.md` "Published image".
- `.env/engine.env` - config (gitignored - `.env/engine.env.example` is the tracked template).
- `.devcontainer/` - Go 1.26 dev container with Docker access; see "Development" below.

## How the console UI talks to the API

It's the same app answering both ports, so the page and the API it calls are always same-origin -
no CORS, no separate backend. The one thing that can't be avoided: since there's no server-side
proxy to do the HMAC signing, **the browser signs its own requests** using the Web Crypto API
(`crypto.subtle`), or a built-in pure-JS HMAC-SHA256 where that's unavailable (see
`engine/wwwroot/app.js`) - byte-for-byte the same
`S3BENDER-HMAC-SHA256` algorithm documented in `_docs/auth-and-signing.md`. The admin key and any
bucket's access/secret key you type in stay in that tab's `localStorage` and are only ever sent to
this app.

Browsers only expose `crypto.subtle` in a secure context (HTTPS, `localhost`, `127.0.0.1`), so on a
plain-HTTP LAN address like `http://192.168.1.20:9067` the console falls back to its pure-JS
signer - it works from any address. Bucket secret keys never leave the browser either way (only
signatures are sent), but over plain HTTP the **admin key** travels in cleartext in every admin
request, so use TLS for anything beyond a trusted LAN (see `_docs/deployment.md`).

Presigned URLs bypass all of this by design: the API returns them with its own public address
(`S3BENDER_PUBLIC_BASE_URL`), and the browser fetches those **directly**, unsigned, until they
expire - that's the point of a presigned URL.

## Development

Open the repo in the dev container (VS Code: "Dev Containers: Reopen in Container") - it has
Go 1.26, the Go extension, and access to the host's Docker. It also sets dev-only
`S3BENDER_ADMIN_API_KEY=dev-admin-key` and a throwaway master key, so this works immediately:

```bash
cd engine
go run .                 # API on :8080, console on :8081 (forwarded), data in engine/data/
go test -race ./...      # unit + end-to-end tests, no external services needed
docker compose up -d --build   # (from the repo root) the production image
```

Without the dev container you only need Go 1.26+; there is no CGO, so no C toolchain either.
F5 in VS Code runs the engine under the debugger (`.vscode/launch.json`).

### Downloads are fast by design

Object GET/HEAD go through `http.ServeContent` on the open file: the body is sent with
`sendfile(2)` (zero-copy), `Range` requests return `206` (videos seek and large images start
rendering immediately), and `If-None-Match`/`If-Modified-Since` return a body-less `304`.
Presigned responses carry `Cache-Control: private, max-age=<remaining lifetime, max 5 min>`, and the
console reuses one presigned URL per object version, so reopening a preview comes straight from the
browser cache.
