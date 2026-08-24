# s3bender

A self-hosted, MinIO-like object storage server: buckets, objects, presigned URLs. One C# /
ASP.NET Core 8 app, Docker-first. The one deliberate difference from MinIO/S3: **every bucket
gets its own access key / secret key pair at creation time**, instead of one shared IAM identity
across all buckets - and unlike S3, that pair can be rotated on demand without touching the
bucket's objects.

Full design and API docs live in [_docs/](_docs/README.md) - read that first, especially
[_docs/auth-and-signing.md](_docs/auth-and-signing.md), before integrating a client. The docs are
written to be language-agnostic - nothing in them is specific to this implementation.

## Quick start

```bash
docker compose up -d --build
curl http://localhost:8080/healthz   # API
curl http://localhost:8081/healthz   # same app, second port - this is what the console UI uses
```

Open http://localhost:8081, paste the admin key from `.env/engine.env`
(`S3BENDER_ADMIN_API_KEY`), create a bucket, and use the returned access/secret key to browse,
upload, download, presign, and rotate objects/credentials - all from the browser. Or drive the API
directly:

```bash
# create a bucket - get back a one-time secret key
curl -X POST http://localhost:8080/admin/buckets \
  -H "X-Admin-Api-Key: $S3BENDER_ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"demo"}'
```

Upload/download requires signing requests with the bucket's own key - see
[_docs/how-to-use.md](_docs/how-to-use.md) for a runnable end-to-end example (bash and
JavaScript, plus what to do if you lose a key), [_docs/auth-and-signing.md](_docs/auth-and-signing.md)
for the exact signing algorithm, or [_docs/presigned-urls.md](_docs/presigned-urls.md) for
generating a URL that needs no signing at the point of use.

## Repo layout

- `engine/` - the C#/.NET 8 implementation: the whole app - bucket CRUD
  (create/list/delete/**rotate**), object PUT/GET/HEAD/DELETE/LIST, presign, HMAC auth, AES-256-GCM
  secret encryption, SQLite metadata + local-disk object storage. `Controllers/`, `Services/`,
  `Middleware/`, `Models/`, `Data/`, `Dtos/`, `Options/`. `wwwroot/` is the static console UI,
  served by this same process on a second port - see "How the console UI talks to the API" below.
  - `Api.Tests/` - xUnit: `SignatureServiceTests` + `WebApplicationFactory` integration tests
    (create→upload→download→presign, and key rotation invalidating the old credential while
    keeping objects intact).
- `_docs/` - the real documentation (architecture, data model, auth spec, API reference,
  how-to-use, deployment, porting guide) - protocol-level, not tied to this implementation.
- `docker-compose.yml` / `docker-compose.override.yml` - base (prod-shaped, pulls a prebuilt
  image) + local dev override (adds `build:`). Runs on `network_mode: host` (Linux) so the
  container's `8080`/`8081` are directly the host's - no explicit port mapping needed.
- `.env/engine.env` - config (gitignored - `.env/engine.env.example` is the tracked template).

## How the console UI talks to the API

It's the same app answering both ports, so the page and the API it calls are always same-origin -
no CORS, no separate backend. The one thing that can't be avoided: since there's no server-side
proxy to do the HMAC signing, **the browser signs its own requests** using the Web Crypto API
(`crypto.subtle`, see `engine/wwwroot/app.js`) - byte-for-byte the same
`S3BENDER-HMAC-SHA256` algorithm documented in `_docs/auth-and-signing.md`. The admin key and any
bucket's access/secret key you type in stay in that tab's `localStorage` and are only ever sent to
this app.

`crypto.subtle` requires a secure context (HTTPS, or `localhost`) - the console works out of the
box in local dev but needs TLS in front of it for any non-localhost deployment (see
`_docs/deployment.md`).

Presigned URLs bypass all of this by design: the API returns them with its own public address
(`S3BENDER_PUBLIC_BASE_URL`), and the browser fetches those **directly**, unsigned, until they
expire - that's the point of a presigned URL.

## Development

```bash
cd engine
dotnet test Api.Tests/Api.Tests.csproj     # needs .NET 8 SDK, or run inside mcr.microsoft.com/dotnet/sdk:8.0
docker build -t s3bender-engine:latest .
```
