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

// View state for the objects list. The server paginates: each fetch returns one ordered page plus
// a cursor for the next. pageCursors[i] is the `cursor` query param that fetches page i (page 0 is
// null); pageIndex is the page on screen. Whole-bucket totals come from a separate /stats call.
const objectsView = { pageSize: 25, pageCursors: [null], pageIndex: 0, totalObjects: 0 };

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
    if (buckets.length === 0) {
      tbody.innerHTML = `<tr><td class="empty-state" colspan="4">No buckets yet - create one above.</td></tr>`;
      return;
    }
    const active = $("bucket-name").value.trim();
    for (const b of buckets) {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td class="key">${escapeHtml(b.name)}${b.name === active ? ' <span class="badge">active</span>' : ""}</td>
        <td class="desc">${b.description ? escapeHtml(b.description) : '<span class="muted">-</span>'}</td>
        <td class="num" title="${escapeHtml(new Date(b.createdAt).toLocaleString())}">${escapeHtml(formatDate(b.createdAt))}</td>
        <td class="actions">
          <div class="btn-row">
            <button class="ghost" data-action="use">Use</button>
            <button class="ghost" data-action="rotate">Rotate keys</button>
            <button class="danger" data-action="delete">Delete</button>
          </div>
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
    $("dialog-bucket-name").value = name;
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

async function createBucket(name, description) {
  const adminKey = $("admin-key").value.trim();
  if (!adminKey) return toast("Enter the admin API key first", true);
  try {
    const bucket = await api("/admin/buckets", {
      method: "POST",
      headers: { "X-Admin-Api-Key": adminKey, "Content-Type": "application/json" },
      body: JSON.stringify(description ? { name, description } : { name }),
    });
    $("secret-dialog-title").textContent = "Bucket created";
    $("dialog-bucket-name").value = bucket.name;
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

function updateActiveBucketBadge() {
  const name = $("bucket-name").value.trim();
  const badge = $("active-bucket-badge");
  if (name) {
    badge.textContent = name;
    badge.classList.remove("empty");
  } else {
    badge.textContent = "none selected";
    badge.classList.add("empty");
  }
}

function selectBucket(name) {
  $("bucket-name").value = name;
  const saved = store.bucketCreds.get(name);
  if (saved) {
    $("access-key").value = saved.accessKey;
    $("secret-key").value = saved.secretKey;
  }
  updateActiveBucketBadge();
  document.querySelector('#objects-table tbody').innerHTML = "";
  objectsView.pageCursors = [null];
  objectsView.pageIndex = 0;
  objectsView.totalObjects = 0;
  $("objects-summary").hidden = true;
  $("objects-pager").hidden = true;
}

// ---- Bucket: objects ----

/** Reset to the first page and refresh both the page and the whole-bucket summary. */
async function listObjects() {
  const { bucket } = currentBucketCreds();
  if (!bucket) return toast("Enter a bucket name first", true);
  objectsView.pageCursors = [null];
  objectsView.pageIndex = 0;
  await Promise.all([fetchObjectPage(), refreshStats()]);
}

/** Fetch and render the page at objectsView.pageIndex, using the cursor recorded for it. */
async function fetchObjectPage() {
  const { bucket } = currentBucketCreds();
  if (!bucket) return;
  const prefix = $("prefix-filter").value.trim();
  const cursor = objectsView.pageCursors[objectsView.pageIndex] || null;
  const params = new URLSearchParams({ limit: String(objectsView.pageSize) });
  if (prefix) params.set("prefix", prefix);
  if (cursor) params.set("cursor", cursor);
  const rawPath = `/buckets/${bucket}/objects`;
  try {
    const resp = await api(`${rawPath}?${params}`, { headers: await bucketAuthHeaders("GET", rawPath) });
    if (resp.isTruncated && objectsView.pageCursors[objectsView.pageIndex + 1] === undefined) {
      objectsView.pageCursors[objectsView.pageIndex + 1] = resp.nextCursor;
    }
    renderObjectsPage(resp);
  } catch (err) {
    toast(`Failed to list objects: ${err.message}`, true);
  }
}

/**
 * Whole-bucket (or whole-prefix) totals for the summary bar. Computed server-side from the object
 * index - the folder/file split is S3-style with delimiter="/": a key with no slash is a
 * top-level file, everything before the first slash is a top-level folder counted once.
 */
async function refreshStats() {
  const { bucket } = currentBucketCreds();
  if (!bucket) return;
  const prefix = $("prefix-filter").value.trim();
  const rawPath = `/buckets/${bucket}/stats`;
  const query = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
  try {
    const s = await api(`${rawPath}${query}`, { headers: await bucketAuthHeaders("GET", rawPath) });
    objectsView.totalObjects = s.objects;
    $("stat-folders").textContent = s.topLevelFolders;
    $("stat-files").textContent = s.topLevelFiles;
    $("stat-objects").textContent = s.objects;
    $("stat-size").textContent = formatBytes(s.totalBytes);
    $("objects-summary").hidden = false;
  } catch (err) {
    toast(`Failed to load bucket stats: ${err.message}`, true);
  }
}

function renderObjectsPage(resp) {
  const { bucket } = currentBucketCreds();
  const prefix = $("prefix-filter").value.trim();
  const tbody = document.querySelector("#objects-table tbody");
  const pager = $("objects-pager");
  const objects = resp.objects;

  tbody.innerHTML = "";
  if (objects.length === 0 && objectsView.pageIndex === 0) {
    tbody.innerHTML = `<tr><td class="empty-state" colspan="6">No objects${prefix ? ` under "${escapeHtml(prefix)}"` : ""}.</td></tr>`;
    pager.hidden = true;
    return;
  }

  for (const o of objects) {
    const tr = document.createElement("tr");
    const publicUrl = `${location.origin}/buckets/${encodeURIComponent(bucket)}/objects/${encodeKeyPath(o.key)}`;
    const etag = o.eTag || "";
    tr.innerHTML = `
      <td class="key" title="${escapeHtml(o.key)}">${escapeHtml(o.key)}</td>
      <td class="num">${formatBytes(o.size)}</td>
      <td class="num" title="${escapeHtml(new Date(o.lastModified).toLocaleString())}">${escapeHtml(formatDate(o.lastModified))}</td>
      <td><span class="badge ${o.public ? "tag-public" : "tag-private"}">${o.public ? "public" : "private"}</span></td>
      <td class="etag"><code data-etag title="${escapeHtml(etag)} - click to copy">${escapeHtml(etag.slice(0, 10))}</code></td>
      <td class="actions">
        <div class="btn-row">
          ${isPreviewable(o.contentType) ? '<button class="ghost" data-action="view">View</button>' : ""}
          <button class="ghost" data-action="download">Download</button>
          ${o.public
            ? '<button class="ghost" data-action="copy-url">Copy URL</button><button class="ghost" data-action="make-private">Make private</button>'
            : '<button class="ghost" data-action="presign">Presign link</button><button class="ghost" data-action="make-public">Make public</button>'}
          <button class="danger" data-action="delete">Delete</button>
        </div>
      </td>`;
    const on = (action, fn) => {
      const btn = tr.querySelector(`[data-action="${action}"]`);
      if (btn) btn.addEventListener("click", fn);
    };
    on("view", () => previewObject(o.key, o.contentType));
    on("download", () => downloadObject(o.key));
    on("presign", () => presignObject(o.key));
    on("copy-url", () => copyText(publicUrl, "Public URL copied to clipboard"));
    on("make-public", () => setObjectAcl(o.key, true));
    on("make-private", () => setObjectAcl(o.key, false));
    on("delete", () => deleteObject(o.key));
    const etagEl = tr.querySelector("[data-etag]");
    if (etagEl && etag) etagEl.addEventListener("click", () => copyText(etag, "ETag copied to clipboard"));
    tbody.appendChild(tr);
  }

  const start = objectsView.pageIndex * objectsView.pageSize;
  const shownEnd = start + objects.length;
  $("pager-info").textContent = objectsView.totalObjects
    ? `${start + 1}–${shownEnd} of ${objectsView.totalObjects}`
    : `${start + 1}–${shownEnd}`;
  $("pager-pos").textContent = `Page ${objectsView.pageIndex + 1}`;
  $("page-prev").disabled = objectsView.pageIndex <= 0;
  $("page-next").disabled = !resp.isTruncated;
  pager.hidden = false;
}

async function uploadFile(file) {
  const { bucket } = currentBucketCreds();
  if (!bucket) return toast("Enter a bucket name first", true);
  const makePublic = $("upload-public").checked;
  const rawPath = `/buckets/${bucket}/objects/${file.name}`;
  try {
    const headers = {
      ...(await bucketAuthHeaders("PUT", rawPath)),
      "Content-Type": file.type || "application/octet-stream",
    };
    if (makePublic) headers["X-S3Bender-Public"] = "true";
    const res = await fetch(`/buckets/${encodeURIComponent(bucket)}/objects/${encodeKeyPath(file.name)}`, {
      method: "PUT",
      headers,
      body: file,
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    toast(`Uploaded "${file.name}"${makePublic ? " (public)" : ""}`);
    $("file-input").value = "";
    await listObjects();
  } catch (err) {
    toast(`Upload failed: ${err.message}`, true);
  }
}

/**
 * Flips an existing object's visibility via PUT /buckets/{bucket}/acl/{key} - no re-upload.
 * Public objects are readable at their plain URL with no credential; private is the default.
 */
async function setObjectAcl(key, makePublic) {
  const { bucket } = currentBucketCreds();
  if (!bucket) return toast("Enter a bucket name first", true);
  const rawPath = `/buckets/${bucket}/acl/${key}`;
  try {
    const res = await fetch(`/buckets/${encodeURIComponent(bucket)}/acl/${encodeKeyPath(key)}`, {
      method: "PUT",
      headers: { ...(await bucketAuthHeaders("PUT", rawPath)), "Content-Type": "application/json" },
      body: JSON.stringify({ public: makePublic }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    toast(`"${key}" is now ${makePublic ? "public" : "private"}`);
    await listObjects();
  } catch (err) {
    toast(`Failed to change visibility: ${err.message}`, true);
  }
}

async function copyText(text, successMessage) {
  try {
    await navigator.clipboard.writeText(text);
    toast(successMessage);
  } catch {
    toast(text);
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

function formatDate(value) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "-";
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
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
  const description = $("new-bucket-description").value.trim();
  if (name) createBucket(name, description);
  $("new-bucket-name").value = "";
  $("new-bucket-description").value = "";
});

$("refresh-buckets").addEventListener("click", refreshBuckets);
$("list-objects").addEventListener("click", listObjects);

$("page-prev").addEventListener("click", () => {
  if (objectsView.pageIndex > 0) {
    objectsView.pageIndex -= 1;
    fetchObjectPage();
  }
});
$("page-next").addEventListener("click", () => {
  objectsView.pageIndex += 1;
  fetchObjectPage();
});
$("page-size").addEventListener("change", (e) => {
  objectsView.pageSize = parseInt(e.target.value, 10) || 25;
  listObjects();
});
$("dialog-close").addEventListener("click", () => $("secret-dialog").close());
$("preview-close").addEventListener("click", () => {
  $("preview-dialog").close();
  $("preview-body").innerHTML = ""; // stop any playing video/audio
});

$("bucket-name").addEventListener("input", updateActiveBucketBadge);
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

updateActiveBucketBadge();

if (store.adminKey.get()) refreshBuckets();
