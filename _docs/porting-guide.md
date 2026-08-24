# Porting guide

If you rewrite s3bender in another language, everything in this directory is the spec to match -
no single codebase is the source of truth. This has already happened once (an earlier Java/Spring
Boot implementation, rewritten to the current `engine/` C#/ASP.NET Core one) without changing
anything in this directory; this page is the checklist that made that possible.

## Must preserve exactly (wire/protocol compatibility)

- **Signing algorithm** - byte-for-byte, see auth-and-signing.md. Any deviation breaks every
  existing presigned URL and every client that has computed a signature against the old server.
- **Endpoint paths, methods, and JSON shapes** - see api-reference.md.
- **Error codes** (the `code` field, e.g. `NoSuchBucket`) - treat these as a stable API contract
  if anything depends on them programmatically, not just the HTTP status.
- **Object key traversal safety** - keys must be validated to stay within their bucket directory
  before touching the filesystem (or equivalent backend). This is a security boundary, not a
  style choice.
- **Access-key-to-bucket binding** - a credential valid for bucket A must be rejected for bucket B
  even with a mathematically correct signature, by checking the resolved bucket name matches the
  URL path.
- **Secret key storage** - encrypted at rest with a server-held master key, never plaintext,
  never merely hashed (it must be decryptable to recompute signatures).
- **Constant-time signature comparison** - a naive string `==`/`.equals` reintroduces a timing
  side-channel.
- **Content-Type persisted on upload, replayed on download** - see data-model.md. Drop this and
  every download silently reverts to `application/octet-stream`, which most browsers will
  download instead of render - breaking `<img>`/`<video>`/`<audio>` embeds and any client that
  relies on presigned URLs being directly embeddable.
- **Key rotation is a pure overwrite, not a read-modify-write of the old secret** - `rotate` must
  generate a new pair and encrypt it under whatever master key is *currently* configured without
  ever decrypting the old `EncryptedSecretKey`. This is what makes rotation double as master-key-
  loss recovery (see how-to-use.md) - get it wrong (e.g. by re-encrypting the *existing* secret
  instead of generating a new one) and that recovery path breaks.

## Free to change (implementation details)

- Language/framework/runtime.
- Metadata storage engine (any embedded or external DB, as long as bucket name and access key stay
  unique and the secret key round-trips through encrypt/decrypt).
- Object storage backend (local disk, S3-compatible blob store, etc.) as long as reads/writes stay
  scoped per-bucket and traversal-safe.
- Internal module/class structure, dependency injection style, config file format.
- Docker base images, build tooling.
- Access/secret key generation format (length, alphabet) - only needs to be unpredictable and
  URL-safe.

## Suggested order for a rewrite

1. Get `/healthz` and bucket CRUD (`/admin/buckets`) working against your metadata store.
2. Implement a signing-service equivalent first, with unit tests (same-input match,
   different-secret mismatch, different-path mismatch) - get this exactly right before wiring it
   into HTTP, since every other endpoint depends on it. See
   `engine/Api.Tests/SignatureServiceTests.cs` for the pattern.
3. Wire the bucket auth filter/middleware for `/buckets/{name}/**`, covering both header and
   presigned-query forms.
4. Implement object PUT/GET/HEAD/DELETE/LIST streaming to your storage backend.
5. Implement `/buckets/{bucket}/presign` and `/admin/buckets/{name}/rotate`.
6. Port (or re-run against the new server) the integration flow in
   `engine/Api.Tests/BucketFlowTests.cs`: create bucket → PUT with header auth → GET with header
   auth → presign → GET with the presigned URL and no auth header at all → rotate the key → confirm
   the old credential is rejected and the object is still readable with the new one. That one test
   file exercises the entire security-relevant path end to end.
