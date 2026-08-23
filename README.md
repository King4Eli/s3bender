# s3bender

A self-hosted, MinIO-like object storage server: buckets, objects, presigned URLs, Java 21 /
Spring Boot, Docker-first. The one deliberate difference from MinIO/S3: **every bucket gets its
own access key / secret key pair at creation time**, instead of one shared IAM identity across
all buckets.

Full design and API docs live in [_docs/](_docs/README.md) - read that first, especially
[_docs/auth-and-signing.md](_docs/auth-and-signing.md), before integrating a client. The docs are
written to be language-agnostic so this can be ported to another stack without losing the
protocol.

## Quick start

```bash
cp .env.example .env   # or use the checked-in dev .env as-is for local testing
docker compose up -d --build
curl http://localhost:8080/healthz
```

```bash
# create a bucket - get back a one-time secret key
curl -X POST http://localhost:8080/admin/buckets \
  -H "X-Admin-Api-Key: $S3BENDER_ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"demo"}'
```

Upload/download requires signing requests with the bucket's own key - see
[_docs/auth-and-signing.md](_docs/auth-and-signing.md) for the exact algorithm, or
[_docs/presigned-urls.md](_docs/presigned-urls.md) for generating a URL that needs no signing at
the point of use.

## Repo layout

- `src/main/java/com/s3bender/` - application code (web/, service/, model/, config/, exception/)
- `src/test/java/` - signing unit tests + a full create-bucket→upload→download→presign integration test
- `_docs/` - the real documentation (architecture, data model, auth spec, API reference, deployment, porting guide)
- `Dockerfile`, `docker-compose.yml` / `docker-compose.override.yml` - containerized build/run
- `.env.example` - config template; `.env` (gitignored) holds real secrets

## Development

```bash
mvn -B test                          # unit + integration tests (needs JDK 21, or run inside the maven:3.9-eclipse-temurin-21 image)
docker build -t s3bender:latest .    # production image
```
