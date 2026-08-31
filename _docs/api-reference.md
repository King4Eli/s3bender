# API reference

Base URL: `http://<host>:8080` (or your `S3BENDER_PUBLIC_BASE_URL`). All request/response bodies
are JSON unless noted. All error bodies:

```json
{ "code": "NoSuchBucket", "message": "Bucket 'demo' does not exist", "timestamp": "2026-08-23T..." }
```

## Health

### `GET /healthz`
No auth. `200 { "status": "ok" }`.

## Admin API — requires `X-Admin-Api-Key: <adminKey>`

### `POST /admin/buckets`
Create a bucket and issue its credentials.

Request: `{ "name": "demo" }` (3-63 chars, lowercase alphanumeric + hyphens, S3-style naming rules)

Response `201`:
```json
{
  "name": "demo",
  "accessKey": "AKXWeDOpNIKa4cZDMp3k-RFw",
  "secretKey": "1iarW14qs22HSKLGKDaPwwsd3Ace8P4fJvbxDiMyuEo",
  "createdAt": "2026-08-23T21:44:33Z"
}
```
`secretKey` is returned **only here** - store it now, it cannot be retrieved or reset again. See
how-to-use.md for what your actual options are if you lose it.

Errors: `409 BucketAlreadyExists`, `400 InvalidRequest` (bad name).

### `GET /admin/buckets`
List buckets (no secrets). `200 [{ "name": "demo", "createdAt": "..." }, ...]`

### `DELETE /admin/buckets/{name}`
Delete a bucket. `204` on success. `409 BucketNotEmpty` if it still has objects - delete every
object first. `404 NoSuchBucket` if it doesn't exist.

### `POST /admin/buckets/{name}/rotate`
Mint a new access/secret key pair for an existing bucket, replacing the old one. Objects are
untouched - this only changes credentials. The old access/secret key (and any outstanding
presigned URL signed with it) stops authenticating **immediately**. Response `200`: same shape as
`POST /admin/buckets` (`name`, `accessKey`, `secretKey`, `createdAt` - the bucket's original
creation time, unchanged). `secretKey` is shown only in this response, same rule as at creation.
`404 NoSuchBucket` if it doesn't exist. This is also the recovery path if a bucket's secret key is
lost - see how-to-use.md.

### `POST /admin/buckets/{name}/reindex`
Rebuild the bucket's object index from what's on disk - walks the bucket directory, re-stats and
re-hashes every file, and reconciles the index rows (adds missing, refreshes changed, drops rows
whose file is gone). Run it once for a bucket that predates the index, or after adding/removing
object files out of band. Cost scales with the bucket's total size (every byte is hashed); safe to
re-run. Response `200 { "bucket": "demo", "indexed": 1423 }` - `indexed` counts rows written or
removed; since this endpoint re-hashes unconditionally, that's every object file plus any stale
rows dropped. `404 NoSuchBucket` if it doesn't exist. The server also runs a non-destructive
version of this in the background at startup (which only touches rows that are actually missing or
changed), so a fresh deployment picks up pre-existing buckets on its own.

## Object API — requires bucket auth (header or presigned; see auth-and-signing.md)

All routes below are under `/buckets/{bucket}`. `{key}` may contain `/` (nested "directories");
it must not contain `..` or start with `/`.

### `PUT /buckets/{bucket}/objects/{key}`
Upload (streamed to disk). Body = raw object bytes. The request's `Content-Type` is stored and
replayed on every future GET/HEAD of this key - set it to the real MIME type (e.g. `image/png`)
if you want the object to render inline (`<img>`, `<video>`, a presigned link opened directly)
instead of falling back to `application/octet-stream`, which most browsers just download.
`200` with header `ETag: "<hex md5>"`. Overwrites an existing object (and its stored
Content-Type and visibility) at the same key.

Optional header `X-S3Bender-Public: true` marks the object public on upload (see
[public-and-private-objects.md](public-and-private-objects.md)). Omitting it, or any value other
than `true`, leaves the object private - this is the default, and re-uploading an existing object
without the header resets it to private even if it was public before.

### `GET /buckets/{bucket}/objects/{key}`
Download. `200`, body = raw bytes, headers `Content-Length`, `ETag`, `Content-Type` (whatever was
set on upload, or `application/octet-stream` if none was), `X-S3Bender-Public` (`true`/`false`).
Accepts header auth or presigned query auth - or, if the object is public, **no auth at all**. See
public-and-private-objects.md. `404 NoSuchKey` if missing.

### `HEAD /buckets/{bucket}/objects/{key}`
Metadata only, no body. Same headers as GET, same auth rules (including the public-object bypass).

### `DELETE /buckets/{bucket}/objects/{key}`
`204` on success (idempotent - deleting a missing key is not an error). Header auth only, always -
visibility never affects this.

### `GET /buckets/{bucket}/objects?prefix=&limit=&cursor=`
List one page of objects, ordered by key. Header auth only. Served from the object index (see
data-model.md), not a directory scan.

Query params (all optional):
- `prefix` - only keys starting with this string.
- `limit` - max objects on the page. Default and hard cap **1000** (S3's max-keys); values outside
  `1..1000` are clamped.
- `cursor` - continuation marker. Pass back the previous response's `nextCursor` to get the next
  page. It is just the last key of that page; the server returns keys strictly greater than it.

Response `200`:
```json
{
  "objects": [
    { "key": "docs/a.txt", "size": 19, "lastModified": "2026-08-23T...", "etag": "3b5fa7...", "contentType": "text/plain", "public": false }
  ],
  "isTruncated": true,
  "nextCursor": "docs/a.txt",
  "keyCount": 1
}
```
`isTruncated` is true when more objects match beyond this page; `nextCursor` is non-null exactly
then. `keyCount` is the number of objects on **this page**, not in the bucket.

An `etag` may be an empty string for a brief window after a server upgrade or an out-of-band file
change, until the background reindex (or the next `HEAD`/`GET` of that object) fills it in.

### `GET /buckets/{bucket}/stats?prefix=<optional>`
Whole-bucket (or whole-prefix) totals, from the index in one query. Header auth only.

```json
{ "objects": 1423, "totalBytes": 90210411, "topLevelFolders": 12, "topLevelFiles": 3 }
```
`topLevelFolders`/`topLevelFiles` count the bucket the way S3 would with `delimiter="/"`: a key
with no `/` is a top-level file; everything before the first `/` is a top-level folder, counted
once however many keys live under it.

### `PUT /buckets/{bucket}/acl/{key}`
Change an existing object's visibility without re-uploading it. Header auth only, always - this is
the one endpoint that can grant public access, so it never accepts the public-object bypass.

Request: `{ "public": true }`

`200` on success. `404 NoSuchKey` if the object doesn't exist.

### `POST /buckets/{bucket}/presign`
Mint a presigned URL. Header auth only. See [presigned-urls.md](presigned-urls.md).

Request: `{ "key": "docs/a.txt", "method": "GET" | "PUT", "expiresInSeconds": 3600 }`

Response: `{ "url": "...", "method": "GET", "expiresAt": "2026-08-23T..." }`

## Error codes

| HTTP | code                | meaning                                             |
|------|---------------------|------------------------------------------------------|
| 400  | `InvalidRequest`     | validation failure (name/key/body shape)             |
| 400  | `InvalidKey`         | object key missing, empty, or attempts traversal     |
| 401  | `Unauthorized`       | missing/malformed admin key or bucket credential      |
| 401  | `InvalidAccessKey`   | access key unknown, or doesn't belong to this bucket  |
| 403  | `SignatureMismatch`  | signature invalid, or timestamp outside allowed skew  |
| 404  | `NoSuchBucket`       | bucket does not exist                                 |
| 404  | `NoSuchKey`          | object does not exist                                 |
| 409  | `BucketAlreadyExists`| bucket name already taken                             |
| 409  | `BucketNotEmpty`     | delete attempted on a non-empty bucket                |
| 500  | `InternalError`      | unexpected server error                               |

A machine-readable OpenAPI document isn't checked in - this table plus the request/response
JSON shapes above is the authoritative contract. If you add an endpoint, update this file in the
same change.
