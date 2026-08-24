# Data model

Two kinds of state, stored separately:

## 1. Bucket metadata (small, structured → embedded database)

One row per bucket:

| field               | type       | notes                                                          |
|---------------------|------------|------------------------------------------------------------------|
| `name`              | string, PK | 3-63 chars, lowercase alphanumeric + hyphens (same rules as S3)  |
| `accessKey`         | string, unique | public identifier, e.g. `AK` + 16 random bytes (base64url)   |
| `encryptedSecretKey`| string     | AES-256-GCM ciphertext of the secret key, base64(iv \|\| ciphertext) |
| `createdAt`         | timestamp  |                                                                    |

The secret key is generated once, returned to the caller exactly once (in the `createBucket`
response), and never stored in plaintext. It must be **decryptable**, not just hashed - the server
needs the raw secret to recompute HMAC signatures for every authenticated request. That's why it's
encrypted (reversible) with a server-held master key, rather than hashed (irreversible) the way a
login password would be.

Losing the master key makes every bucket's *existing* secret key permanently unrecoverable -
existing presigned URLs and stored credentials stop validating. It's not a dead end, though: a
key-rotation endpoint (`POST /admin/buckets/{name}/rotate`, admin-key only) issues a fresh secret
encrypted under whatever master key is currently set, without ever needing to decrypt the old one
- so rotating every bucket under a replacement master key recovers all of them, objects untouched.
See how-to-use.md. Back up `S3BENDER_MASTER_KEY` regardless - see deployment.md.

## 2. Object bytes (large, unstructured → filesystem)

Objects are not in the database at all. They live directly on disk at
`{storage root}/{bucket}/{key}`, and their metadata (size, last-modified, ETag) is derived on
read from the filesystem, not stored redundantly. ETag = hex MD5 of the object's bytes (matches S3
convention for non-multipart uploads, used here purely as an integrity fingerprint, not a security
control).

One piece of upload-supplied metadata *is* stored, since it can't be derived from the bytes: the
`Content-Type` given on `PUT`. It lives as a small sidecar text file at
`{storage root}/.meta/{bucket}/{key}` - deliberately outside `{bucket}/`, so it never appears in
a bucket listing or counts toward "is this bucket empty" - and is replayed verbatim as the
`Content-Type` response header on every future GET/HEAD of that key (falling back to
`application/octet-stream` if no sidecar exists, e.g. for objects uploaded before this existed).
This is what lets a presigned URL be dropped straight into an `<img>`/`<video>`/`<audio>` tag and
render instead of download - see presigned-urls.md.

A reimplementation is free to swap the embedded database for anything else (SQLite, Postgres, a
JSON file, etcd) as long as it preserves: bucket name uniqueness, access key uniqueness, and the
ability to decrypt a bucket's secret key given its row. It is free to swap local disk for S3/GCS/
etc. as the object backend as long as key → bytes lookup stays scoped per-bucket and traversal-safe.
