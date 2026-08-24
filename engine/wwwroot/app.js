const $ = (id) => document.getElementById(id);

const store = {
  adminKey: {
    get: () => localStorage.getItem("s3bender.adminKey") || "",
    set: (v) => localStorage.setItem("s3bender.adminKey", v),
  },
  bucketCreds: {
    get: (name) => JSON.parse(localStorage.getItem(`s3bender.bucket.${name}`) || "null"),
    set: (name, creds) => localStorage.setItem(`s3bender.bucket.${name}`, JSON.stringify(creds)),
    remove: (name) => localStorage.removeItem(`s3bender.bucket.${name}`),
  },
};

function toast(message, isError = false) {
  const el = $("toast");
  el.textContent = message;
  el.classList.remove("hidden");
  el.classList.toggle("error", isError);
  clearTimeout(toast._t);
  toast._t = setTimeout(() => el.classList.add("hidden"), 4000);
}

function encodeKeyPath(key) {
  return key.split("/").map(encodeURIComponent).join("/");
}

async function api(pathAndQuery, options = {}) {
  const res = await fetch(pathAndQuery, options);
  const contentType = res.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await res.json().catch(() => null) : await res.text();
  if (!res.ok) {
    const message = (body && body.message) || (typeof body === "string" && body) || `HTTP ${res.status}`;
    throw new Error(message);
  }
  return body;
}

// ---- Signing (S3BENDER-HMAC-SHA256, see /_docs/auth-and-signing.md) ----
//
// This page is served by the same app it talks to (both ports 8080 and 8081 answer the same
// process), so there's no separate proxy to do the signing for us - the browser signs its own
// requests with the Web Crypto API. StringToSign is METHOD + "\n" + PATH + "\n" + TIMESTAMP,
// where PATH is the raw (unencoded) request path; the actual fetch URL still needs its key
// segments percent-encoded (see encodeKeyPath above), but the string that gets signed never is.

async function hmacSha256Hex(secret, message) {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", key, enc.encode(message));
  return Array.from(new Uint8Array(sig)).map((b) => b.toString(16).padStart(2, "0")).join("");
}

async function buildAuthHeader(accessKey, secretKey, method, rawPath) {
  const timestamp = Math.floor(Date.now() / 1000);
  const stringToSign = `${method}\n${rawPath}\n${timestamp}`;
  const signature = await hmacSha256Hex(secretKey, stringToSign);
  return `S3BENDER-HMAC-SHA256 AccessKey=${accessKey},Timestamp=${timestamp},Signature=${signature}`;
}

function currentBucketCreds() {
  return {
    bucket: $("bucket-name").value.trim(),
    accessKey: $("access-key").value.trim(),
    secretKey: $("secret-key").value,
  };
}

async function bucketAuthHeaders(method, rawPath) {
  const { accessKey, secretKey } = currentBucketCreds();
  return { Authorization: await buildAuthHeader(accessKey, secretKey, method, rawPath) };
}

// ---- Admin: buckets ----

async function refreshBuckets() {
  const adminKey = $("admin-key").value.trim();
  if (!adminKey) return toast("Enter the admin API key first", true);
  try {
    const buckets = await api("/admin/buckets", { headers: { "X-Admin-Api-Key": adminKey } });
    const tbody = document.querySelector("#buckets-table tbody");
    tbody.innerHTML = "";
    for (const b of buckets) {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${escapeHtml(b.name)}</td>
        <td>${new Date(b.createdAt).toLocaleString()}</td>
        <td>
          <button data-action="use">Use</button>
          <button data-action="rotate">Rotate keys</button>
          <button data-action="delete">Delete</button>
        </td>`;
      tr.querySelector('[data-action="use"]').addEventListener("click", () => selectBucket(b.name));
      tr.querySelector('[data-action="rotate"]').addEventListener("click", () => rotateBucketKey(b.name));
      tr.querySelector('[data-action="delete"]').addEventListener("click", () => deleteBucket(b.name));
      tbody.appendChild(tr);
    }
  } catch (err) {
    toast(`Failed to list buckets: ${err.message}`, true);
  }
}

/**
 * Mints a new access/secret key pair for a bucket, invalidating the old one immediately
 * (including any outstanding presigned URLs signed with it) - objects are untouched. This is
 * also the recovery path if a bucket's secret key is lost - see /_docs/how-to-use.md.
 */
async function rotateBucketKey(name) {
  const adminKey = $("admin-key").value.trim();
  if (!adminKey) return toast("Enter the admin API key first", true);
  if (!confirm(`Rotate keys for "${name}"? The current access/secret key stops working immediately.`)) return;
  try {
    const bucket = await api(`/admin/buckets/${encodeURIComponent(name)}/rotate`, {
      method: "POST",
      headers: { "X-Admin-Api-Key": adminKey },
    });
    $("secret-dialog-title").textContent = "Keys rotated";
    $("dialog-access-key").value = bucket.accessKey;
    $("dialog-secret-key").value = bucket.secretKey;
    $("secret-dialog").showModal();

    if (store.bucketCreds.get(name)) {
      store.bucketCreds.set(name, { accessKey: bucket.accessKey, secretKey: bucket.secretKey });
    }
    if ($("bucket-name").value.trim() === name) {
      $("access-key").value = bucket.accessKey;
      $("secret-key").value = bucket.secretKey;
    }
    toast(`Rotated keys for "${name}"`);
  } catch (err) {
    toast(`Failed to rotate keys: ${err.message}`, true);
  }
}

async function createBucket(name) {
  const adminKey = $("admin-key").value.trim();
  if (!adminKey) return toast("Enter the admin API key first", true);
  try {
    const bucket = await api("/admin/buckets", {
      method: "POST",
      headers: { "X-Admin-Api-Key": adminKey, "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
    $("secret-dialog-title").textContent = "Bucket created";
    $("dialog-access-key").value = bucket.accessKey;
    $("dialog-secret-key").value = bucket.secretKey;
    $("secret-dialog").showModal();

    selectBucket(bucket.name);
    $("access-key").value = bucket.accessKey;
    $("secret-key").value = bucket.secretKey;
    if ($("remember-creds").checked) {
      store.bucketCreds.set(bucket.name, { accessKey: bucket.accessKey, secretKey: bucket.secretKey });
    }
    await refreshBuckets();
  } catch (err) {
    toast(`Failed to create bucket: ${err.message}`, true);
  }
}

async function deleteBucket(name) {
  const adminKey = $("admin-key").value.trim();
  if (!adminKey) return toast("Enter the admin API key first", true);
  if (!confirm(`Delete bucket "${name}"? It must be empty.`)) return;
  try {
    await api(`/admin/buckets/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: { "X-Admin-Api-Key": adminKey },
    });
    store.bucketCreds.remove(name);
    toast(`Deleted bucket "${name}"`);
    await refreshBuckets();
  } catch (err) {
    toast(`Failed to delete bucket: ${err.message}`, true);
  }
}

function selectBucket(name) {
  $("bucket-name").value = name;
  const saved = store.bucketCreds.get(name);
  if (saved) {
    $("access-key").value = saved.accessKey;
    $("secret-key").value = saved.secretKey;
  }
  document.querySelector('#objects-table tbody').innerHTML = "";
}

// ---- Bucket: objects ----

async function listObjects() {
  const { bucket } = currentBucketCreds();
  if (!bucket) return toast("Enter a bucket name first", true);
  const prefix = $("prefix-filter").value.trim();
  const query = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
  const rawPath = `/buckets/${bucket}/objects`;
  try {
    const objects = await api(`${rawPath}${query}`, {
      headers: await bucketAuthHeaders("GET", rawPath),
    });
    const tbody = document.querySelector("#objects-table tbody");
    tbody.innerHTML = "";
    for (const o of objects) {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${escapeHtml(o.key)}</td>
        <td>${formatBytes(o.size)}</td>
        <td>${new Date(o.lastModified).toLocaleString()}</td>
        <td class="etag">${o.etag}</td>
        <td>
          ${isPreviewable(o.contentType) ? '<button data-action="view">View</button>' : ""}
          <button data-action="download">Download</button>
          <button data-action="presign">Presign link</button>
          <button data-action="delete">Delete</button>
        </td>`;
      const viewBtn = tr.querySelector('[data-action="view"]');
      if (viewBtn) viewBtn.addEventListener("click", () => previewObject(o.key, o.contentType));
      tr.querySelector('[data-action="download"]').addEventListener("click", () => downloadObject(o.key));
      tr.querySelector('[data-action="presign"]').addEventListener("click", () => presignObject(o.key));
      tr.querySelector('[data-action="delete"]').addEventListener("click", () => deleteObject(o.key));
      tbody.appendChild(tr);
    }
    if (objects.length === 0) toast("No objects found");
  } catch (err) {
    toast(`Failed to list objects: ${err.message}`, true);
  }
}

async function uploadFile(file) {
  const { bucket } = currentBucketCreds();
  if (!bucket) return toast("Enter a bucket name first", true);
  const rawPath = `/buckets/${bucket}/objects/${file.name}`;
  try {
    const res = await fetch(`/buckets/${encodeURIComponent(bucket)}/objects/${encodeKeyPath(file.name)}`, {
      method: "PUT",
      headers: { ...(await bucketAuthHeaders("PUT", rawPath)), "Content-Type": file.type || "application/octet-stream" },
      body: file,
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    toast(`Uploaded "${file.name}"`);
    $("file-input").value = "";
    await listObjects();
  } catch (err) {
    toast(`Upload failed: ${err.message}`, true);
  }
}

async function downloadObject(key) {
  const { bucket } = currentBucketCreds();
  const rawPath = `/buckets/${bucket}/objects/${key}`;
  try {
    const res = await fetch(`/buckets/${encodeURIComponent(bucket)}/objects/${encodeKeyPath(key)}`, {
      headers: await bucketAuthHeaders("GET", rawPath),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = key.split("/").pop();
    a.click();
    URL.revokeObjectURL(url);
  } catch (err) {
    toast(`Download failed: ${err.message}`, true);
  }
}

async function requestPresign(bucket, key) {
  const rawPath = `/buckets/${bucket}/presign`;
  return api(`/buckets/${encodeURIComponent(bucket)}/presign`, {
    method: "POST",
    headers: { ...(await bucketAuthHeaders("POST", rawPath)), "Content-Type": "application/json" },
    body: JSON.stringify({ key, method: "GET", expiresInSeconds: 300 }),
  });
}

async function presignObject(key) {
  const { bucket } = currentBucketCreds();
  try {
    const result = await requestPresign(bucket, key);
    await navigator.clipboard.writeText(result.url).catch(() => {});
    toast(`Presigned link copied to clipboard (expires ${new Date(result.expiresAt).toLocaleTimeString()})`);
  } catch (err) {
    toast(`Presign failed: ${err.message}`, true);
  }
}

function isPreviewable(contentType) {
  if (!contentType) return false;
  return /^(image|video|audio)\//.test(contentType) || contentType === "application/pdf" || contentType.startsWith("text/");
}

/**
 * Renders the object inline using a short-lived presigned GET URL - the same mechanism a
 * client embedding an <img>/<video> tag would use, since those elements can't send an
 * Authorization header themselves.
 */
async function previewObject(key, contentType) {
  const { bucket } = currentBucketCreds();
  try {
    const result = await requestPresign(bucket, key);

    $("preview-title").textContent = key;
    const body = $("preview-body");
    body.innerHTML = "";

    let el;
    if (contentType.startsWith("image/")) {
      el = document.createElement("img");
      el.src = result.url;
    } else if (contentType.startsWith("video/")) {
      el = document.createElement("video");
      el.src = result.url;
      el.controls = true;
    } else if (contentType.startsWith("audio/")) {
      el = document.createElement("audio");
      el.src = result.url;
      el.controls = true;
    } else {
      // PDF and text render fine in an iframe; anything else falls back to browser handling.
      el = document.createElement("iframe");
      el.src = result.url;
    }
    body.appendChild(el);
    $("preview-dialog").showModal();
  } catch (err) {
    toast(`Preview failed: ${err.message}`, true);
  }
}

async function deleteObject(key) {
  const { bucket } = currentBucketCreds();
  const rawPath = `/buckets/${bucket}/objects/${key}`;
  if (!confirm(`Delete "${key}"?`)) return;
  try {
    const res = await fetch(`/buckets/${encodeURIComponent(bucket)}/objects/${encodeKeyPath(key)}`, {
      method: "DELETE",
      headers: await bucketAuthHeaders("DELETE", rawPath),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    toast(`Deleted "${key}"`);
    await listObjects();
  } catch (err) {
    toast(`Delete failed: ${err.message}`, true);
  }
}

// ---- helpers ----

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
  return `${n.toFixed(1)} ${units[i]}`;
}

// ---- wire up ----

$("admin-key").value = store.adminKey.get();
$("admin-key").addEventListener("change", (e) => store.adminKey.set(e.target.value.trim()));

$("create-bucket-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const name = $("new-bucket-name").value.trim();
  if (name) createBucket(name);
  $("new-bucket-name").value = "";
});

$("refresh-buckets").addEventListener("click", refreshBuckets);
$("list-objects").addEventListener("click", listObjects);
$("dialog-close").addEventListener("click", () => $("secret-dialog").close());
$("preview-close").addEventListener("click", () => {
  $("preview-dialog").close();
  $("preview-body").innerHTML = ""; // stop any playing video/audio
});

$("bucket-name").addEventListener("change", (e) => {
  const saved = store.bucketCreds.get(e.target.value.trim());
  if (saved) {
    $("access-key").value = saved.accessKey;
    $("secret-key").value = saved.secretKey;
  }
});

$("file-input").addEventListener("change", (e) => {
  const file = e.target.files[0];
  if (file) uploadFile(file);
});

if (!crypto.subtle) {
  toast("Web Crypto (crypto.subtle) is unavailable - this page must be served over HTTPS or localhost.", true);
}

if (store.adminKey.get()) refreshBuckets();
