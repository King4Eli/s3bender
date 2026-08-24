# Deployment

## Quick start

```bash
cp .env/engine.env.example .env/engine.env       # fill in real secrets, or generate (see below)
docker compose up -d --build
curl http://localhost:8080/healthz    # API
curl http://localhost:8081/healthz    # same app, second port - the console UI lives here
```

`docker-compose.yml` is the base file (prod-shaped: pulls the `s3bender-engine:latest` image
rather than building it) and `docker-compose.override.yml` layers in `build:` for local dev -
`docker compose` loads both automatically. It runs one service, on `network_mode: host` (so the
container shares the host's network stack directly - no Docker bridge networking, no port
mapping needed):

| service           | container name    | ports          | env file           |
|--------------------|--------------------|----------------|----------------------|
| `s3bender-engine`  | `s3bender-engine`  | `8080`, `8081` | `.env/engine.env`  |

Both ports are the same process on two Kestrel endpoints: `8080` is the plain API, `8081`
additionally serves the static console UI at `/` - the full REST API is reachable on either port,
since they share one pipeline. See the root README's "How the console UI talks to the Api" for why
no separate frontend service or proxy is needed.

`network_mode: host` is Linux-only; on Docker Desktop (Mac/Windows) switch back to bridge
networking with explicit `ports: ["8080:8080", "8081:8081"]` mappings instead.

Object bytes and the metadata database persist in the named volume `file-storage` (survives
`docker compose down`; use `down -v` to wipe it).

## Environment variables

See `.env/engine.env.example` for the full file.

| variable                    | required | purpose                                                        |
|------------------------------|----------|------------------------------------------------------------------|
| `S3BENDER_ADMIN_API_KEY`     | yes      | shared secret for `/admin/*`. Generate with `openssl rand -base64 24`. |
| `S3BENDER_MASTER_KEY`        | yes      | AES-256 key (base64, must decode to exactly 32 bytes) encrypting bucket secret keys at rest. Generate with `openssl rand -base64 32`. Losing it is recoverable per-bucket via key rotation - see how-to-use.md - but back it up anyway. |
| `S3BENDER_PUBLIC_BASE_URL`   | recommended | host[:port] the *browser* can reach, used when building presigned URLs. Without it, presigned URLs default to whatever host/port the incoming request appears to target, which can be wrong behind a reverse proxy. |
| `S3BENDER_FRONTEND_PORT`     | no       | the console UI's second Kestrel endpoint (default `8081`).       |
| `S3BENDER_CLOCK_SKEW_SECONDS` | no       | allowed timestamp drift for header auth (default `900`).        |
| `S3BENDER_MAX_PRESIGN_EXPIRY_SECONDS` | no | cap on presigned URL lifetime (default `604800`, 7 days).  |

The Api refuses to start without a valid 32-byte `S3BENDER_MASTER_KEY` - fail closed rather than
run with unusable encryption.

## TLS

The container serves plain HTTP on both ports. Two separate reasons this matters:

- Because request bodies are excluded from the signature (see auth-and-signing.md), an
  unencrypted transport lets an on-path attacker read/replay traffic within a signature's
  validity window.
- The console UI's client-side request signing depends on `crypto.subtle` (Web Crypto), which
  browsers only expose in a *secure context* - HTTPS, or `localhost`. The UI will silently fail
  to sign anything if served over plain HTTP on a non-localhost host.

**Always terminate TLS in front of this** - a reverse proxy (nginx, Caddy, Traefik) or your cloud
load balancer - before exposing either port outside a trusted network. Set
`S3BENDER_PUBLIC_BASE_URL` to the public `https://` origin so presigned URLs come out correct.

## Storage and backups

- Object bytes: `{S3BENDER_STORAGE_ROOT}` (default `/data/objects` inside the container, backed by
  the `file-storage` volume). Back up like any file store.
- Metadata DB: `{S3BENDER_DB_PATH}` (default `/data/db/s3bender.db`, same volume). Losing this
  loses every bucket's access/secret key pair even though the object bytes on disk are intact -
  back it up alongside the object data, not separately.
- The master key is deliberately **not** stored in the volume - keep it in your secrets manager /
  `.env/engine.env`, backed up independently of the data volume.

## Scaling notes

This is a single-node design: one filesystem, one SQLite file. It does not support running
multiple replicas against the same storage root concurrently (no distributed locking). To scale
beyond one node, either put it behind a single active instance with the volume on network-attached
storage that supports safe single-writer access, or treat multi-node support as a feature to
design deliberately (e.g. swap the object backend for S3/GCS and the metadata store for a real
shared database) rather than something to fake with this setup.

## Building without Docker

Requires the .NET 8 SDK:

```bash
cd engine
dotnet run
```
