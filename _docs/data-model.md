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

Losing the master key makes every bucket's secret key permanently unrecoverable - existing
presigned URLs and stored credentials stop validating. Back up `S3BENDER_MASTER_KEY` accordingly.

## 2. Object bytes (large, unstructured → filesystem)

Objects are not in the database at all. They live directly on disk at
`{storage root}/{bucket}/{key}`, and their metadata (size, last-modified, ETag) is derived on
read from the filesystem, not stored redundantly. ETag = hex MD5 of the object's bytes (matches S3
convention for non-multipart uploads, used here purely as an integrity fingerprint, not a security
control).

A reimplementation is free to swap the embedded database for anything else (SQLite, Postgres, a
JSON file, etcd) as long as it preserves: bucket name uniqueness, access key uniqueness, and the
ability to decrypt a bucket's secret key given its row. It is free to swap local disk for S3/GCS/
etc. as the object backend as long as key → bytes lookup stays scoped per-bucket and traversal-safe.
