# Architecture

## Components

```
                        ┌─────────────────────────────┐
  admin (X-Admin-Api-   │                              │
  Key) ─────────────────▶  Admin API  (/admin/buckets) │
                        │                              │
                        └──────────────┬───────────────┘
                                       │ creates
                                       ▼
                        ┌─────────────────────────────┐
                        │  Bucket metadata (buckets)   │
                        │  name, accessKey,            │
                        │  encryptedSecretKey          │
                        └──────────────┬───────────────┘
                                       │ used to authenticate
                                       ▼
  client (bucket creds  ┌─────────────────────────────┐
  or presigned URL) ────▶  Object API (/buckets/{b}/…) │──▶ filesystem: {root}/{bucket}/{key}
                        └─────────────────────────────┘
```

A single server process exposes two credential-scoped APIs:

- **Admin API** (`/admin/*`) - create, list, delete buckets. Guarded by one shared secret
  (`S3BENDER_ADMIN_API_KEY`). This is the only credential that spans buckets.
- **Object API** (`/buckets/{bucket}/*`) - upload, download, list, delete objects, and mint
  presigned URLs. Guarded by that bucket's own access/secret key pair. A credential for bucket A
  can never authenticate a request against bucket B - the request path names the bucket, and the
  filter checks that the access key on the request actually belongs to that bucket.

## Request flow

1. Admin calls `POST /admin/buckets` with `X-Admin-Api-Key`. Server generates a random
   `accessKey` + `secretKey`, encrypts the secret key at rest, stores the pair, creates a
   directory for the bucket, and returns the secret key **once** (never again, and never in list
   responses).
2. Client authenticates every `/buckets/{bucket}/*` request one of two ways:
   - **Header auth**: `Authorization: S3BENDER-HMAC-SHA256 AccessKey=…,Timestamp=…,Signature=…`
     - used for uploads, downloads, deletes, listing, and requesting a presigned URL.
   - **Presigned query string**: `?AccessKey=…&Expires=…&Signature=…`
     - used for GET/PUT/HEAD only, generated ahead of time by `POST /buckets/{bucket}/presign`
       (itself a header-authenticated call), and valid without any header at all until `Expires`.
3. A single auth filter sits in front of every `/buckets/**` route, resolves the bucket from the
   URL path, resolves the credential (header or query), verifies the HMAC signature, checks
   expiry/clock-skew, and only then lets the request reach the object handler.
4. Object bytes are streamed straight to/from disk - the server never buffers a whole file in
   memory. Metadata (bucket name → keys) lives in a small embedded database; object bytes live in
   ordinary files.

See [auth-and-signing.md](auth-and-signing.md) for the exact byte-for-byte signing algorithm.

## The console UI is a client, not a third API

`engine/wwwroot/` is a small static single-page app served by the same process as the API,
on a second Kestrel endpoint (`S3BENDER_FRONTEND_PORT`, default 8081) - both ports answer the
exact same pipeline, so the console and the raw API are always same-origin. It has no privileged
access: it signs its own `/buckets/**` requests client-side with the Web Crypto API using whatever
access/secret key the person using it types in, exactly like any other client would have to. There
is no server-side proxy or credential store sitting between the browser and the API - see the root
README's "How the console UI talks to the Api".

A middleware narrows what's reachable per port, but only in one direction: the console's own
static assets (`/`, `/index.html`, `/app.js`, `/styles.css`) 404 on the plain API port (8080),
since external API clients have no use for them. Every real API route - `/admin/**` included -
deliberately stays reachable on **both** ports, because the console has a full Admin panel
(create/list/delete/rotate buckets) as well as the object browser, and both call the API
same-origin from whichever port served the page. This is a deployment convenience, not a protocol
requirement - the console is just another client of the same API documented in api-reference.md. A
reimplementation is free to serve it differently (a separate proxying service, a static site
elsewhere, or dropped entirely) without touching the protocol.

## Storage layout

```
{S3BENDER_STORAGE_ROOT}/
  {bucket-name}/
    {object-key}            # object keys may contain '/' and are stored as nested paths
    docs/hello.txt
    images/2026/a.png

{S3BENDER_DB_PATH}           # embedded DB file holding bucket metadata only, not object bytes
```

Object keys are validated so they cannot escape their bucket directory (no `..`, no leading `/`,
no NUL bytes). This is the only thing standing between a malicious key and path traversal, so any
reimplementation must keep this check.

## Why per-bucket credentials (vs. one global root user)

MinIO and AWS S3 use a global identity system (IAM users/policies) layered on top of buckets.
s3bender intentionally skips that: each bucket is a fully independent security boundary with its
own key pair, minted at creation time and usable only for that bucket. This trades away
fine-grained policy (no "read-only" vs "read-write" roles, no cross-bucket users) for a much
simpler mental model: **one bucket = one tenant = one credential**. If you need shared
cross-bucket identities or scoped permissions later, that's a deliberate, separate feature - don't
bolt it on by weakening the per-bucket boundary.
