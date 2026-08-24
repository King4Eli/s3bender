# How to use

Two ways to call s3bender: through the console UI (port 8081) if you just want to click around, or
directly against the API (port 8080, also reachable on 8081) if you're writing a client. This page
covers the direct-API path in bash and JavaScript - a runnable example of everything in
auth-and-signing.md and api-reference.md strung together - plus what your actual options are if
you lose a key.

## Calling the API from scratch

Everything below assumes `S3BENDER_ADMIN_API_KEY` is in your environment and the Api is reachable
at `http://localhost:8080`.

**1. Create a bucket.** This is the only step that uses the admin key instead of a signature:

```bash
curl -s -X POST http://localhost:8080/admin/buckets \
  -H "X-Admin-Api-Key: $S3BENDER_ADMIN_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"demo"}'
# {"name":"demo","accessKey":"AK...","secretKey":"...","createdAt":"..."}
```

Save `accessKey` and `secretKey` from the response - `secretKey` is never shown again.

**2. Sign a request.** Every `/buckets/{bucket}/**` call needs an `Authorization` header built
from those two values. This is the same computation `engine/Services/SignatureService.cs`
does server-side and the console's own `app.js` does client-side via Web Crypto - see
auth-and-signing.md for the exact string-to-sign layout this reproduces:

```bash
ACCESS_KEY="AK..."      # from step 1
SECRET_KEY="..."        # from step 1
sign() {
  local method="$1" path="$2" ts="$3"
  printf '%s\n%s\n%s' "$method" "$path" "$ts" \
    | openssl dgst -sha256 -hmac "$SECRET_KEY" -hex | sed 's/^.* //'
}
```

**3. Upload an object:**

```bash
PATH1="/buckets/demo/objects/docs/hello.txt"
TS=$(date +%s)
SIG=$(sign PUT "$PATH1" "$TS")
curl -X PUT "http://localhost:8080$PATH1" \
  -H "Authorization: S3BENDER-HMAC-SHA256 AccessKey=$ACCESS_KEY,Timestamp=$TS,Signature=$SIG" \
  -H "Content-Type: text/plain" \
  --data-binary "hello s3bender"
```

Set `Content-Type` to the real MIME type if you want the object to render inline later instead of
downloading - see api-reference.md.

**4. Download it back:**

```bash
TS=$(date +%s)
SIG=$(sign GET "$PATH1" "$TS")
curl "http://localhost:8080$PATH1" \
  -H "Authorization: S3BENDER-HMAC-SHA256 AccessKey=$ACCESS_KEY,Timestamp=$TS,Signature=$SIG"
```

**5. Mint a presigned URL** (no signing needed to *use* it, just to request it):

```bash
PPATH="/buckets/demo/presign"
TS=$(date +%s)
SIG=$(sign POST "$PPATH" "$TS")
curl -X POST "http://localhost:8080$PPATH" \
  -H "Authorization: S3BENDER-HMAC-SHA256 AccessKey=$ACCESS_KEY,Timestamp=$TS,Signature=$SIG" \
  -H "Content-Type: application/json" \
  -d '{"key":"docs/hello.txt","method":"GET","expiresInSeconds":300}'
# {"url":"http://localhost:8080/buckets/demo/objects/docs/hello.txt?AccessKey=...&Expires=...&Signature=...", ...}
```

That `url` works with a plain `curl "$url"` - no header at all - until it expires. See
presigned-urls.md.

**Common mistake:** a clock more than `S3BENDER_CLOCK_SKEW_SECONDS` (default 900s) off wall-clock
time produces a `403 SignatureMismatch` even with a correct secret - the timestamp is part of what
gets signed, so an out-of-sync clock signs a value the server won't accept.

## JavaScript: request a presigned upload, then upload from the browser

The right split for a web app: your own trusted backend (which holds the bucket's secret key)
requests the presigned URL; the browser only ever sees that one-time URL, never the secret. This
is the same shape as the console's own `app.js`, just split across two sides of a network boundary
instead of both happening client-side.

**Backend (Node.js) - request a presigned PUT URL:**

```js
const crypto = require("crypto");

function sign(secret, stringToSign) {
  return crypto.createHmac("sha256", secret).update(stringToSign).digest("hex");
}

async function presignUpload(bucket, key, accessKey, secretKey) {
  const rawPath = `/buckets/${bucket}/presign`;
  const timestamp = Math.floor(Date.now() / 1000);
  const signature = sign(secretKey, `POST\n${rawPath}\n${timestamp}`);

  const res = await fetch(`http://localhost:8080${rawPath}`, {
    method: "POST",
    headers: {
      Authorization: `S3BENDER-HMAC-SHA256 AccessKey=${accessKey},Timestamp=${timestamp},Signature=${signature}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ key, method: "PUT", expiresInSeconds: 300 }),
  });
  if (!res.ok) throw new Error(`presign failed: ${res.status}`);
  return (await res.json()).url; // hand this back to the browser, not the secret key
}
```

**Browser - upload directly using that URL, no credentials needed:**

```js
async function uploadViaPresignedUrl(presignedUrl, file) {
  const res = await fetch(presignedUrl, {
    method: "PUT",
    headers: { "Content-Type": file.type || "application/octet-stream" },
    body: file, // a File/Blob from an <input type="file"> or drag-and-drop
  });
  if (!res.ok) throw new Error(`upload failed: ${res.status}`);
}

// <input type="file" id="picker">
document.getElementById("picker").addEventListener("change", async (e) => {
  const file = e.target.files[0];
  const presignedUrl = await fetch("/your-backend/presign-upload-url").then((r) => r.text());
  await uploadViaPresignedUrl(presignedUrl, file);
});
```

The browser never imports a crypto library or touches the secret key - it just does a plain
`fetch` PUT to a URL your backend handed it. Downloads work the same way with `method: "GET"` and
a plain `fetch(presignedUrl)` (or just setting it as an `<img src>` - see api-reference.md's note
on `Content-Type`).

If you'd rather have the *browser itself* sign requests directly against the API (no backend
round-trip, at the cost of the browser needing to hold the bucket's secret key) - that's exactly
what the console UI does; see `engine/wwwroot/app.js` for the complete Web Crypto
(`crypto.subtle`) implementation of the same signing algorithm shown in bash above.

## Losing a key

"I lost it" has a different answer depending on which key:

**Admin key (`S3BENDER_ADMIN_API_KEY`)** - trivial. It's a config value, not derived from
anything stored. Generate a new one (`openssl rand -base64 24`), update `.env/engine.env`, restart
the Api. No data is affected; existing buckets and their credentials are untouched, since the
admin key only gates `/admin/*`.

**A bucket's secret key** - not recoverable (it's encrypted, not stored, and never shown twice -
see data-model.md), but it doesn't need to be: anyone holding the *admin* key can mint the bucket
a fresh one without touching its objects:

```bash
curl -X POST http://localhost:8080/admin/buckets/demo/rotate \
  -H "X-Admin-Api-Key: $S3BENDER_ADMIN_API_KEY"
# {"name":"demo","accessKey":"AK...","secretKey":"...","createdAt":"..."}   <- new pair, once
```

This immediately invalidates the old access/secret key and every presigned URL signed with it -
see presigned-urls.md. The console UI has the same thing as a "Rotate keys" button per bucket.

If you *also* don't have the admin key (you're just an API client, not the operator): it's
genuinely gone, and there's no admin override to force-empty or force-delete a bucket you don't
hold credentials for. Ask whoever operates the server to rotate it for you, or write off the
bucket and create a new one. An operator with direct database access could alternatively decrypt
the stored `EncryptedSecretKey` offline using `S3BENDER_MASTER_KEY` (see
`CryptoService.DecryptSecret` in `engine/Services/`) if recovering the *exact* old key
matters for some reason - but rotating is simpler and is the intended path.

**Master key (`S3BENDER_MASTER_KEY`)** - sounds catastrophic (every bucket's secret key becomes
undecryptable at once, not just one - see data-model.md) but is recoverable the same way, and for
the same reason: rotation *writes* a fresh secret encrypted under whatever master key is
*currently* configured, it never needs to decrypt the old one. So after losing the master key:

1. Set a new `S3BENDER_MASTER_KEY` and restart the Api.
2. Rotate every existing bucket (`POST /admin/buckets/{name}/rotate` for each one, admin key
   only) and redistribute the new credentials.

Objects are untouched throughout - this has been verified end to end (create bucket → upload →
lose the master key → rotate → fetch the same object back with the new credentials). The only
things that don't survive the loss are the *old* credentials themselves (already unusable the
moment the master key changed) and any bucket you never get around to rotating. Back the master
key up somewhere durable and separate from the data volume regardless - see deployment.md -
rotating every bucket by hand is a real recovery path, not a reason to skip backups.
