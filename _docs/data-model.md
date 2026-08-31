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

Object *bytes* are never in the database. They live directly on disk at
`{storage root}/{bucket}/{key}`. ETag = hex MD5 of the object's bytes (matches S3 convention for
non-multipart uploads, used here purely as an integrity fingerprint, not a security control),
computed once as the bytes stream past on `PUT`.

Two pieces of upload-supplied metadata plus that hash are stored in a small JSON sidecar file at
`{storage root}/.meta/{bucket}/{key}` - `{"contentType": "image/png", "public": false, "eTag":
"..."}` - deliberately outside `{bucket}/`, so it never appears in a directory walk or counts
toward "is this bucket empty". `contentType` is replayed verbatim as the `Content-Type` response
header on every future GET/HEAD of that key (falling back to `application/octet-stream` if absent).
This is what lets a presigned URL - or a public object's plain URL - be dropped straight into an
`<img>`/`<video>`/`<audio>` tag and render instead of download - see presigned-urls.md. `public` is
what `BucketAuthMiddleware` checks to decide whether a GET/HEAD needs a valid signature at all.

A sidecar written before the `public` field existed is a bare Content-Type string, not JSON; it's
read back as `{contentType: <that string>, public: false}` rather than failing to parse, so
pre-existing objects stay private by default. A sidecar written before `eTag` was added simply has
no such field, and the hash is recomputed once on next access and written back.

## 3. Object index (queryable → embedded database)

One row per object, in the same embedded database as the bucket table:

| field          | type              | notes                                                        |
|----------------|-------------------|--------------------------------------------------------------|
| `bucket`, `key`| string, composite PK | ordered by `key` within a `bucket` - this PK *is* the listing index |
| `size`         | integer           |                                                              |
| `lastModified` | timestamp         | file mtime as of indexing                                    |
| `eTag`         | string, nullable  | null only transiently (a row a reindex has discovered but not yet hashed) |
| `contentType`  | string, nullable  |                                                              |
| `public`       | boolean           |                                                              |

This table is a **cache, not a source of truth** - the bytes and the sidecar are. It exists so a
listing is `WHERE bucket = ? AND key > ? ORDER BY key LIMIT ?` over the PK instead of walking and
`stat`-ing (and, before the ETag was cached, MD5-hashing) the entire bucket directory on every
call. Whole-bucket totals (`GET /buckets/{bucket}/stats`) are `COUNT`/`SUM` over it.

Kept in sync three ways: written inline on every `PUT`/`DELETE`/ACL change; rebuilt for one bucket
on demand by `POST /admin/buckets/{name}/reindex` (and by a non-destructive background pass at
startup); and self-healed on the first listing of a bucket that has files on disk but no rows
(e.g. the database file was restored without them). Because it's derived, deleting every object row
and reindexing loses nothing.

## Reimplementation notes

A reimplementation is free to swap the embedded database for anything else (SQLite, Postgres, a
JSON file, etcd) as long as it preserves: bucket name uniqueness, access key uniqueness, the
ability to decrypt a bucket's secret key given its row, and a per-`(bucket, key)` object lookup
that can be scanned in key order. It may drop the object index entirely and walk the filesystem
per listing instead - correct, just O(bucket size) per call. It is free to swap local disk for
S3/GCS/etc. as the object backend as long as key → bytes lookup stays scoped per-bucket and
traversal-safe.
