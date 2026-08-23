# s3bender docs

s3bender is a self-hosted, MinIO-like object storage server: buckets, objects, presigned URLs.
The one deliberate departure from MinIO/S3: **every bucket has its own access key / secret key
pair**, generated when the bucket is created, instead of a single set of IAM credentials shared
across all buckets.

These docs describe the *system*, not the Java code, so the design survives a rewrite in another
language. Read them in this order:

1. [architecture.md](architecture.md) - components, request flow, storage layout.
2. [data-model.md](data-model.md) - what's persisted and where.
3. [auth-and-signing.md](auth-and-signing.md) - the two credential types and the exact signing
   algorithm (byte-for-byte spec, language-independent).
4. [presigned-urls.md](presigned-urls.md) - how presigned URLs are built and validated.
5. [api-reference.md](api-reference.md) - every HTTP endpoint, request/response shapes, error codes.
6. [deployment.md](deployment.md) - Docker/Compose, environment variables, operational notes.
7. [porting-guide.md](porting-guide.md) - what to preserve if you reimplement this in another
   language/stack, and what's safe to change.

The current implementation is Java 21 / Spring Boot 3, but nothing in the wire protocol or data
model is Java-specific - see porting-guide.md.
