# Public and private objects

Every object has a visibility flag, checked independently of everything else on the object:

- **Private (default).** Every read or write needs a valid credential - a signed `Authorization`
  header or a presigned URL. This is what every object gets unless you say otherwise.
- **Public.** `GET`/`HEAD` work with **no credential at all** - a plain URL anyone can fetch, cache,
  or hand to a browser, forever (or until the object is made private again). Every other operation
  on it (`PUT`, `DELETE`, list, changing the ACL) still requires a valid credential regardless.

This is the same shape as S3's `public-read` ACL, and it's the answer to "how do I get a URL that
doesn't expire and can sit behind a CDN or browser cache": a presigned URL is deliberately temporary
and re-signed on every mint (see presigned-urls.md), so it's the wrong tool for a URL you want to
stay valid and cacheable indefinitely. A public object's plain URL is stable - same URL, every time,
no expiry - which is exactly what lets a CDN or browser cache it effectively.

## Marking an object public

Examples below reuse the `authHeader` helper from how-to-use.md (Node.js `fetch` + `node:crypto`).

**At upload time**, add the `X-S3Bender-Public: true` header to the `PUT`:

```js
const path1 = "/buckets/demo/objects/logo.png";
await fetch(`${BASE}${path1}`, {
  method: "PUT",
  headers: {
    Authorization: authHeader(accessKey, secretKey, "PUT", path1),
    "Content-Type": "image/png",
    "X-S3Bender-Public": "true",
  },
  body: logoPngBuffer,
});
```

**On an existing object**, without re-uploading it, via the ACL endpoint:

```js
const aclPath = "/buckets/demo/acl/logo.png";
await fetch(`${BASE}${aclPath}`, {
  method: "PUT",
  headers: {
    Authorization: authHeader(accessKey, secretKey, "PUT", aclPath),
    "Content-Type": "application/json",
  },
  body: JSON.stringify({ public: true }),
});
```

Same endpoint with `{ public: false }` makes it private again - immediately; the very next `GET`
with no credential gets `401 InvalidAccessKey` instead of the object.

**Important:** visibility is replaced, not merged, on every `PUT`. Re-uploading a public object
without `X-S3Bender-Public: true` on that specific request resets it to private - the same as S3
not carrying an ACL forward across a fresh upload. If you're re-uploading a public object's
content, pass the header again, or set the ACL back afterward.

## Retrieving a private object

Same as always - either:

1. **A signed request**, using the bucket's access/secret key pair (see auth-and-signing.md):

   ```js
   const path2 = "/buckets/demo/objects/docs/report.pdf";
   const res = await fetch(`${BASE}${path2}`, {
     headers: { Authorization: authHeader(accessKey, secretKey, "GET", path2) },
   });
   ```

2. **A presigned URL**, minted via `POST /buckets/{bucket}/presign` and then used with no header at
   all, until it expires (see presigned-urls.md):

   ```js
   const res = await fetch(presignedUrl);
   ```

A private object rejects a plain unsigned request with `401 InvalidAccessKey` (no credential
supplied) or `403 SignatureMismatch` (credential supplied but invalid/expired).

## Retrieving a public object

Just the plain URL, no signing, no presign step, no `Authorization` header:

```js
const res = await fetch("http://localhost:8080/buckets/demo/objects/logo.png");
```

or drop it straight into `<img src="...">` , share it, put a CDN in front of it, let a browser
cache it - the URL never expires and never changes, so ordinary HTTP caching (`ETag`, browser
cache, CDN edge cache) works on it the way it would on any other static asset. Signed requests and
presigned URLs for the same object still work too, if you happen to send one - visibility only
*relaxes* the requirement, it never blocks the normal authenticated paths.

Every response - public or private - includes `X-S3Bender-Public: true`/`false` and the object's
`ETag`, so a client can tell which case it's in. Note the server doesn't currently honor
`If-None-Match` itself (every GET returns the full body, `200`) - a CDN or browser cache sitting in
front of a public object's URL can still use the `ETag` for its own revalidation logic, but
s3bender won't answer a conditional request with `304` on its own.

## How this is enforced

`BucketAuthMiddleware` checks the object's visibility *before* it even looks for an `AccessKey` -
for a `GET`/`HEAD` on a path shaped like `/buckets/{bucket}/objects/{key}`, if
`ObjectStorageService.IsPublic(bucket, key)` is true, the request is dispatched immediately with no
signature check. Every other verb, and every request for a private object, falls through to the
normal credential check (header or presigned) exactly as before - see auth-and-signing.md. Because
the check is per-object, you can freely mix public and private objects in the same bucket.
