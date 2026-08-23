# Deployment

## Quick start

```bash
cp .env.example .env        # then fill in real values, or use the generated dev .env as-is locally
docker compose up -d --build
curl http://localhost:8080/healthz
```

`docker-compose.yml` builds the image, exposes port `${8080}`, loads config from
`.env`, and persists both object bytes and the metadata database in the named volume
`file-storage` (survives `docker compose down`; use `down -v` to wipe it).

## Environment variables

All configuration is env-first (`.env`, loaded via `env_file` in Compose) - see `.env.example`
for the full list. The two that matter most:

| variable                    | required | purpose                                                        |
|------------------------------|----------|------------------------------------------------------------------|
| `S3BENDER_ADMIN_API_KEY`     | yes      | shared secret for `/admin/*`. Generate with `openssl rand -base64 24`. |
| `S3BENDER_MASTER_KEY`        | yes      | AES-256 key (base64, must decode to exactly 32 bytes) encrypting bucket secret keys at rest. Generate with `openssl rand -base64 32`. **Back this up separately from the data volume** - losing it makes every bucket's secret key unrecoverable. |
| `S3BENDER_PUBLIC_BASE_URL`   | no       | overrides the host used when building presigned URLs; set this behind a reverse proxy. |
| `8080`               | no       | host port to publish (default `8080`).                          |
| `S3BENDER_CLOCK_SKEW_SECONDS` | no       | allowed timestamp drift for header auth (default `900`).        |
| `S3BENDER_MAX_PRESIGN_EXPIRY_SECONDS` | no | cap on presigned URL lifetime (default `604800`, 7 days).  |

The server refuses to start without `S3BENDER_ADMIN_API_KEY` and a valid 32-byte
`S3BENDER_MASTER_KEY` - fail closed rather than run with an unset admin key or unusable
encryption.

## TLS

The container serves plain HTTP on 8080. Because request bodies are excluded from the signature
(see auth-and-signing.md), an unencrypted transport lets an on-path attacker read/replay traffic
within the signature's validity window. **Always terminate TLS in front of this** - a reverse
proxy (nginx, Caddy, Traefik) or your cloud load balancer - before exposing it outside a trusted
network. Set `S3BENDER_PUBLIC_BASE_URL` to the public `https://` origin so presigned URLs come out
correct.

## Storage and backups

- Object bytes: `{S3BENDER_STORAGE_ROOT}` (default `/data/objects` inside the container, backed by
  the `file-storage` volume). Back up like any file store.
- Metadata DB: `{S3BENDER_DB_PATH}` (default `/data/db/s3bender`, same volume). Losing this loses
  every bucket's access/secret key pair even though the object bytes on disk are intact - back it
  up alongside the object data, not separately.
- The master key is deliberately **not** stored in the volume - keep it in your secrets manager /
  `.env`, backed up independently of the data volume.

## Scaling notes

This is a single-node design: one filesystem, one embedded database file. It does not
support running multiple replicas against the same storage root concurrently (no distributed
locking). To scale beyond one node, either put it behind a single active instance with the volume
on network-attached storage that supports safe single-writer access, or treat multi-node support
as a feature to design deliberately (e.g. swap the object backend for S3/GCS and the metadata
store for a real shared database) rather than something to fake with this setup.

## Building without Docker

Requires JDK 21 + Maven:

```bash
mvn -B package -DskipTests
S3BENDER_ADMIN_API_KEY=... S3BENDER_MASTER_KEY=... java -jar target/s3bender.jar
```
