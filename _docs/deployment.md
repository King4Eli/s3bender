# Deployment

## Quick start

On a server, pull the published image (no source checkout or build needed - just
`docker-compose.yml` and `.env/engine.env`):

```bash
cp .env/engine.env.example .env/engine.env       # fill in real secrets, or generate (see below)
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
curl http://localhost:9066/healthz    # API
curl http://localhost:9067/healthz    # same app, second port - the console UI lives here
```

For local development, plain `docker compose up -d --build` also loads
`docker-compose.override.yml`, which adds `build:` so the image is built from `engine/` instead
of pulled.

| service    | container name | host ports → container          | env file          |
|------------|----------------|----------------------------------|-------------------|
| `s3bender` | `s3bender`     | `9066` → `8080`, `9067` → `8081` | `.env/engine.env` |

Both container ports are the same process on two HTTP listeners: `8080` is the plain API, `8081`
additionally serves the static console UI at `/` - the full REST API is reachable on either port,
since they share one pipeline. See the root README's "How the console UI talks to the Api" for why
no separate frontend service or proxy is needed.

## Published image (GitHub Actions → GHCR)

`.github/workflows/image.yml` runs the Go tests and then builds a multi-arch
(`linux/amd64` + `linux/arm64`) image and pushes it to `ghcr.io/king4eli/s3bender`:

| trigger              | tags pushed                                   |
|----------------------|-----------------------------------------------|
| push to `main`       | `latest`, `main`, `sha-<short>`               |
| push tag `v1.2.3`    | `1.2.3`, `1.2`, `1`, `sha-<short>`            |
| pull request         | none - tests + build only                     |

It authenticates with the workflow's built-in `GITHUB_TOKEN`, so no secrets need configuring.
Pin a deployment with `IMAGE_TAG` (e.g. `IMAGE_TAG=1.2.3 docker compose -f docker-compose.yml up -d`);
it defaults to `latest`.

New GHCR packages are **private**. Either make it public (GitHub → your profile → Packages →
`s3bender` → Package settings → Change visibility), or log the server in once with a personal
access token that has the `read:packages` scope:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u king4eli --password-stdin
```

Object bytes and the metadata database persist in the named volume `file-storage` (survives
`docker compose down`; use `down -v` to wipe it).

## Environment variables

See `.env/engine.env.example` for the full file.

| variable                    | required | purpose                                                        |
|------------------------------|----------|------------------------------------------------------------------|
| `S3BENDER_ADMIN_API_KEY`     | yes      | shared secret for `/admin/*`. Generate with `openssl rand -base64 24`. |
| `S3BENDER_MASTER_KEY`        | yes      | AES-256 key (base64, must decode to exactly 32 bytes) encrypting bucket secret keys at rest. Generate with `openssl rand -base64 32`. Losing it is recoverable per-bucket via key rotation - see how-to-use.md - but back it up anyway. |
| `S3BENDER_PUBLIC_BASE_URL`   | recommended | host[:port] the *browser* can reach, used when building presigned URLs. Without it, presigned URLs default to whatever host/port the incoming request appears to target, which can be wrong behind a reverse proxy. |
| `S3BENDER_FRONTEND_PORT`     | no       | the console UI's second HTTP listener (default `8081`).         |
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

Requires Go 1.26+ (no CGO / C toolchain needed), or use the dev container in `.devcontainer/`:

```bash
cd engine
go run .            # or: go build -o s3bender . && ./s3bender
```

The binary is self-contained - the console UI is embedded - so it can be copied anywhere and run
with just the environment variables above.

## Upgrading from the C# engine

Drop-in: the Go engine reads the same SQLite schema, timestamp format, encrypted secrets and
sidecar files, and the image runs as the same uid/gid (999), so an existing `file-storage` volume
and `.env/engine.env` work unchanged - rebuild/pull the image and `docker compose up -d`. Existing
bucket credentials and outstanding presigned URLs stay valid.
