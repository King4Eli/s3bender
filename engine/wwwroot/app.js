"use strict";

const $ = (id) => document.getElementById(id);

// ---- persistence (localStorage can throw in private windows - never let that break the page) ----

const BUCKET_PREFIX = "s3bender.bucket.";

function lsGet(k) { try { return localStorage.getItem(k); } catch { return null; } }
function lsSet(k, v) { try { localStorage.setItem(k, v); } catch { /* ignore */ } }
function lsDel(k) { try { localStorage.removeItem(k); } catch { /* ignore */ } }

const store = {
  adminKey: {
    get: () => lsGet("s3bender.adminKey") || "",
    set: (v) => (v ? lsSet("s3bender.adminKey", v) : lsDel("s3bender.adminKey")),
  },
  bucketCreds: {
    get: (name) => { try { return JSON.parse(lsGet(BUCKET_PREFIX + name) || "null"); } catch { return null; } },
    set: (name, creds) => lsSet(BUCKET_PREFIX + name, JSON.stringify(creds)),
    remove: (name) => lsDel(BUCKET_PREFIX + name),
    names: () => {
      const out = [];
      try {
        for (let i = 0; i < localStorage.length; i++) {
          const k = localStorage.key(i);
          if (k && k.startsWith(BUCKET_PREFIX)) out.push(k.slice(BUCKET_PREFIX.length));
        }
      } catch { /* ignore */ }
      return out.sort();
    },
  },
};

// ---- view state ----

const state = {
  buckets: [],      // last admin listing: [{ name, description, createdAt }]
  bucket: null,     // the open bucket's credentials: { name, accessKey, secretKey }
  folder: "",       // current folder within the bucket: "" or ends with "/"
  filter: "",       // name-prefix filter within the folder
  pageSize: 25,
  pageCursors: [null], // pageCursors[i] is the `cursor` that fetches page i
  pageIndex: 0,
  totalObjects: 0,
  listSeq: 0,       // drops responses that arrive after a newer listing was started
};

const listPrefix = () => state.folder + state.filter;

// ---- icons ----

const ICONS = {
  file: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>',
  doc: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-5-5L5 21"/>',
  video: '<rect x="2" y="5" width="15" height="14" rx="2"/><path d="m22 8-5 4 5 4z"/>',
  audio: '<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>',
  bucket: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  more: '<circle cx="5" cy="12" r="1.3"/><circle cx="12" cy="12" r="1.3"/><circle cx="19" cy="12" r="1.3"/>',
  eye: '<path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  download: '<path d="M12 4v12m0 0-4-4m4 4 4-4"/><path d="M4 18v1a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-1"/>',
};

const icon = (name) =>
  `<svg class="i" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name]}</svg>`;

function fileKind(contentType = "") {
  if (contentType.startsWith("image/")) return "image";
  if (contentType.startsWith("video/")) return "video";
  if (contentType.startsWith("audio/")) return "audio";
  if (contentType.startsWith("text/") || contentType === "application/pdf" || contentType.includes("json")) return "doc";
  return "file";
}

// ---- small helpers ----

function toast(message, isError = false) {
  const el = $("toast");
  el.textContent = message;
  el.classList.remove("hidden");
  el.classList.toggle("error", isError);
  clearTimeout(toast._t);
  toast._t = setTimeout(() => el.classList.add("hidden"), 4000);
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function encodeKeyPath(key) {
  return key.split("/").map(encodeURIComponent).join("/");
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

async function copyText(text, successMessage) {
  try {
    // navigator.clipboard, like crypto.subtle, only exists in a secure context.
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(text);
    else legacyCopy(text);
    toast(successMessage);
  } catch {
    toast(text);
  }
}

/** Clipboard copy that also works on plain-HTTP LAN addresses. */
function legacyCopy(text) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;top:0;left:0;opacity:0";
  (document.querySelector("dialog[open]") || document.body).appendChild(ta);
  ta.select();
  const ok = document.execCommand("copy");
  ta.remove();
  if (!ok) throw new Error("copy failed");
}

async function api(pathAndQuery, options = {}) {
  const res = await fetch(pathAndQuery, options);
  const contentType = res.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await res.json().catch(() => null) : await res.text();
  if (!res.ok) {
    const message = (body && body.message) || (typeof body === "string" && body) || `HTTP ${res.status}`;
    const err = new Error(message);
    err.status = res.status;
    throw err;
  }
  return body;
}

const adminHeaders = (extra = {}) => ({ "X-Admin-Api-Key": store.adminKey.get(), ...extra });

// ---- Signing (S3BENDER-HMAC-SHA256, see /_docs/auth-and-signing.md) ----
//
// This page is served by the same app it talks to (both ports 8080 and 8081 answer the same
// process), so there's no separate proxy to do the signing for us - the browser signs its own
// requests with the Web Crypto API. StringToSign is METHOD + "\n" + PATH + "\n" + TIMESTAMP,
// where PATH is the raw (unencoded) request path; the actual fetch URL still needs its key
// segments percent-encoded (see encodeKeyPath above), but the string that gets signed never is.

async function hmacSha256Hex(secret, message) {
  const enc = new TextEncoder();
  let sig;
  if (window.crypto?.subtle) {
    const key = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
    sig = new Uint8Array(await crypto.subtle.sign("HMAC", key, enc.encode(message)));
  } else {
    sig = hmacSha256(enc.encode(secret), enc.encode(message));
  }
  return Array.from(sig).map((b) => b.toString(16).padStart(2, "0")).join("");
}

// Browsers only expose crypto.subtle in a secure context (HTTPS, localhost, 127.0.0.1) - not on
// plain http://192.168.x.x or any other LAN address. This pure-JS SHA-256/HMAC (FIPS 180-4,
// RFC 2104) is the fallback there, so the console can sign requests from any address. Speed is
// irrelevant: it only ever hashes a key and a ~100-byte string-to-sign.
const SHA256_K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function sha256(bytes) {
  const len = bytes.length;
  const padded = new Uint8Array(((len + 9 + 63) >> 6) << 6);
  padded.set(bytes);
  padded[len] = 0x80;
  const dv = new DataView(padded.buffer);
  dv.setUint32(padded.length - 8, Math.floor(len / 0x20000000)); // bit length, high word
  dv.setUint32(padded.length - 4, (len << 3) >>> 0);             // bit length, low word

  const H = new Uint32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
  const W = new Uint32Array(64);
  const rotr = (x, n) => (x >>> n) | (x << (32 - n));
  for (let off = 0; off < padded.length; off += 64) {
    for (let i = 0; i < 16; i++) W[i] = dv.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(W[i - 15], 7) ^ rotr(W[i - 15], 18) ^ (W[i - 15] >>> 3);
      const s1 = rotr(W[i - 2], 17) ^ rotr(W[i - 2], 19) ^ (W[i - 2] >>> 10);
      W[i] = W[i - 16] + s0 + W[i - 7] + s1;
    }
    let [a, b, c, d, e, f, g, h] = H;
    for (let i = 0; i < 64; i++) {
      const t1 = (h + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + SHA256_K[i] + W[i]) | 0;
      const t2 = ((rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      h = g; g = f; f = e; e = (d + t1) | 0;
      d = c; c = b; b = a; a = (t1 + t2) | 0;
    }
    H[0] += a; H[1] += b; H[2] += c; H[3] += d; H[4] += e; H[5] += f; H[6] += g; H[7] += h;
  }
  const out = new Uint8Array(32);
  const odv = new DataView(out.buffer);
  H.forEach((v, i) => odv.setUint32(i * 4, v));
  return out;
}

function hmacSha256(key, message) {
  if (key.length > 64) key = sha256(key);
  const ipad = new Uint8Array(64 + message.length);
  const opad = new Uint8Array(64 + 32);
  for (let i = 0; i < 64; i++) {
    const k = key[i] || 0;
    ipad[i] = k ^ 0x36;
    opad[i] = k ^ 0x5c;
  }
  ipad.set(message, 64);
  opad.set(sha256(ipad), 64);
  return sha256(opad);
}

async function buildAuthHeader(accessKey, secretKey, method, rawPath) {
  const timestamp = Math.floor(Date.now() / 1000);
  const stringToSign = `${method}\n${rawPath}\n${timestamp}`;
  const signature = await hmacSha256Hex(secretKey, stringToSign);
  return `S3BENDER-HMAC-SHA256 AccessKey=${accessKey},Timestamp=${timestamp},Signature=${signature}`;
}

async function bucketAuthHeaders(method, rawPath) {
  const { accessKey, secretKey } = state.bucket;
  return { Authorization: await buildAuthHeader(accessKey, secretKey, method, rawPath) };
}

/** Raw (signed) path and percent-encoded URL path for an object key in the open bucket. */
function objectPaths(kind, key) {
  const b = state.bucket.name;
  return { raw: `/buckets/${b}/${kind}/${key}`, url: `/buckets/${encodeURIComponent(b)}/${kind}/${encodeKeyPath(key)}` };
}

// ---- routing: #/ (buckets) and #/b/{bucket}/{folder...} ----

function bucketHref(name, folder = "") {
  return `#/b/${encodeURIComponent(name)}${folder ? "/" + encodeKeyPath(folder) : ""}`;
}

function parseRoute() {
  const m = location.hash.match(/^#\/b\/([^/]+)(?:\/(.*))?$/);
  if (!m) return { view: "home" };
  try {
    return {
      view: "bucket",
      bucket: decodeURIComponent(m[1]),
      folder: m[2] ? m[2].split("/").map(decodeURIComponent).join("/") : "",
    };
  } catch {
    return { view: "home" };
  }
}

function render() {
  closeMenu();
  const route = parseRoute();
  if (route.view === "bucket") openBucket(route.bucket, route.folder);
  else showHome();
}

function setCrumbs(parts) {
  // parts: [{ label, href? }] - last one is the current location.
  $("crumbs").innerHTML = parts
    .map((p, i) => {
      const last = i === parts.length - 1;
      const item = last ? `<span class="current">${escapeHtml(p.label)}</span>` : `<a href="${p.href}">${escapeHtml(p.label)}</a>`;
      return (i ? '<span class="sep">/</span>' : "") + item;
    })
    .join("");
}

function showView(id) {
  for (const v of document.querySelectorAll(".view")) v.hidden = v.id !== id;
}

// ---- admin key ----

function updateAdminChip() {
  const on = !!store.adminKey.get();
  $("admin-dot").classList.toggle("on", on);
  $("admin-status").textContent = on ? "Admin connected" : "Admin key not set";
}

function setAdminKey(key) {
  store.adminKey.set(key.trim());
  updateAdminChip();
  if (parseRoute().view === "home") showHome();
}

// ---- home: bucket cards ----

async function showHome() {
  showView("view-buckets");
  setCrumbs([{ label: "Buckets" }]);
  document.title = "Buckets · s3bender";

  const hasAdmin = !!store.adminKey.get();
  $("admin-gate").hidden = hasAdmin;
  $("new-bucket").disabled = !hasAdmin;

  if (!hasAdmin) {
    renderBucketCards(store.bucketCreds.names().map((name) => ({ name })), false);
    return;
  }
  try {
    state.buckets = await api("/admin/buckets", { headers: adminHeaders() });
    renderBucketCards(state.buckets, true);
  } catch (err) {
    if (err.status === 401) {
      toast("The saved admin key was rejected - enter it again", true);
      $("admin-gate").hidden = false;
    } else {
      toast(`Failed to list buckets: ${err.message}`, true);
    }
    renderBucketCards(store.bucketCreds.names().map((name) => ({ name })), false);
  }
}

function renderBucketCards(buckets, fromAdmin) {
  const grid = $("bucket-grid");
  grid.innerHTML = "";
  if (buckets.length === 0) {
    grid.innerHTML = fromAdmin
      ? `<div class="empty"><strong>No buckets yet</strong>Create one with <em>New bucket</em> above.</div>`
      : `<div class="empty"><strong>No saved buckets</strong>Connect as admin, or use <em>Open with keys</em>.</div>`;
    return;
  }
  for (const b of buckets) {
    const saved = !!store.bucketCreds.get(b.name);
    const card = document.createElement("article");
    card.className = "bucket-card";
    card.tabIndex = 0;
    card.innerHTML = `
      <div class="card-top">
        <span class="card-icon">${icon("bucket")}</span>
        <button class="icon-btn" type="button" data-menu aria-label="Bucket actions">${icon("more")}</button>
      </div>
      <h3 title="${escapeHtml(b.name)}">${escapeHtml(b.name)}</h3>
      <p class="desc">${b.description ? escapeHtml(b.description) : fromAdmin ? '<span class="muted">No description</span>' : ""}</p>
      <div class="card-meta">
        <span>${b.createdAt ? `Created ${escapeHtml(formatDate(b.createdAt))}` : "Saved in this browser"}</span>
        <span class="badge ${saved ? "ok" : ""}">${saved ? "keys saved" : "no keys"}</span>
      </div>`;
    const open = () => (location.hash = bucketHref(b.name));
    card.addEventListener("click", (e) => { if (!e.target.closest("[data-menu]")) open(); });
    card.addEventListener("keydown", (e) => { if (e.key === "Enter" && e.target === card) open(); });
    card.querySelector("[data-menu]").addEventListener("click", (e) => {
      e.stopPropagation();
      const items = [{ label: "Open", action: open }];
      if (fromAdmin) items.push({ label: "Rotate keys…", action: () => rotateBucketKey(b.name) });
      if (saved) items.push({ label: "Forget saved keys", action: () => { store.bucketCreds.remove(b.name); showHome(); } });
      if (fromAdmin) items.push("sep", { label: "Delete bucket…", danger: true, action: () => deleteBucket(b.name) });
      openMenu(e.currentTarget, items);
    });
    grid.appendChild(card);
  }
}

function showSecret(title, bucket) {
  $("secret-dialog-title").textContent = title;
  $("dialog-bucket-name").value = bucket.name;
  $("dialog-access-key").value = bucket.accessKey;
  $("dialog-secret-key").value = bucket.secretKey;
  $("secret-dialog").showModal();
}

async function createBucket(name, description, remember) {
  try {
    const bucket = await api("/admin/buckets", {
      method: "POST",
      headers: adminHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify(description ? { name, description } : { name }),
    });
    const creds = { name: bucket.name, accessKey: bucket.accessKey, secretKey: bucket.secretKey };
    if (remember) store.bucketCreds.set(bucket.name, { accessKey: creds.accessKey, secretKey: creds.secretKey });
    state.bucket = creds;
    state.buckets = [...state.buckets.filter((x) => x.name !== bucket.name), bucket];
    location.hash = bucketHref(bucket.name);
    showSecret("Bucket created", bucket);
  } catch (err) {
    toast(`Failed to create bucket: ${err.message}`, true);
  }
}

/**
 * Mints a new access/secret key pair for a bucket, invalidating the old one immediately
 * (including any outstanding presigned URLs signed with it) - objects are untouched. This is
 * also the recovery path if a bucket's secret key is lost - see /_docs/how-to-use.md.
 */
async function rotateBucketKey(name) {
  if (!confirm(`Rotate keys for "${name}"? The current access/secret key stops working immediately.`)) return;
  try {
    const bucket = await api(`/admin/buckets/${encodeURIComponent(name)}/rotate`, { method: "POST", headers: adminHeaders() });
    if (store.bucketCreds.get(name)) store.bucketCreds.set(name, { accessKey: bucket.accessKey, secretKey: bucket.secretKey });
    if (state.bucket && state.bucket.name === name) state.bucket = { name, accessKey: bucket.accessKey, secretKey: bucket.secretKey };
    showSecret("Keys rotated", bucket);
    toast(`Rotated keys for "${name}"`);
    if (parseRoute().view === "home") showHome();
  } catch (err) {
    toast(`Failed to rotate keys: ${err.message}`, true);
  }
}

async function deleteBucket(name) {
  if (!confirm(`Delete bucket "${name}"? It must be empty.`)) return;
  try {
    await api(`/admin/buckets/${encodeURIComponent(name)}`, { method: "DELETE", headers: adminHeaders() });
    store.bucketCreds.remove(name);
    if (state.bucket && state.bucket.name === name) state.bucket = null;
    toast(`Deleted bucket "${name}"`);
    if (parseRoute().view === "home") showHome();
    else location.hash = "#/";
  } catch (err) {
    toast(`Failed to delete bucket: ${err.message}`, true);
  }
}

// ---- keys dialog ----

let keysResolve = null;

/** Ask for a bucket's keys. Resolves to { name, accessKey, secretKey } or null if cancelled. */
function promptKeys({ name = "", lockName = false, title = "Bucket keys", action = "Open bucket" } = {}) {
  const saved = name ? store.bucketCreds.get(name) : null;
  $("keys-title").textContent = title;
  $("keys-bucket").value = name;
  $("keys-bucket").readOnly = lockName;
  $("keys-access").value = saved?.accessKey || (state.bucket?.name === name ? state.bucket.accessKey : "");
  $("keys-secret").value = saved?.secretKey || (state.bucket?.name === name ? state.bucket.secretKey : "");
  $("keys-remember").checked = true;
  $("keys-form").querySelector('button[value="ok"]').textContent = action;
  $("keys-dialog").showModal();
  (name ? $("keys-access") : $("keys-bucket")).focus();
  return new Promise((resolve) => (keysResolve = resolve));
}

$("keys-dialog").addEventListener("close", () => {
  const ok = $("keys-dialog").returnValue === "ok";
  const result = ok
    ? { name: $("keys-bucket").value.trim(), accessKey: $("keys-access").value.trim(), secretKey: $("keys-secret").value }
    : null;
  if (result && $("keys-remember").checked) {
    store.bucketCreds.set(result.name, { accessKey: result.accessKey, secretKey: result.secretKey });
  } else if (result) {
    store.bucketCreds.remove(result.name);
  }
  $("keys-dialog").returnValue = "";
  const resolve = keysResolve;
  keysResolve = null;
  if (resolve) resolve(result);
});

// ---- bucket view ----

async function openBucket(name, folder) {
  if (!state.bucket || state.bucket.name !== name) {
    const saved = store.bucketCreds.get(name);
    if (saved) {
      state.bucket = { name, ...saved };
    } else {
      const creds = await promptKeys({ name, lockName: true, title: `Keys for ${name}` });
      if (!creds) {
        location.hash = "#/";
        return;
      }
      state.bucket = { ...creds, name };
    }
    state.filter = "";
    $("prefix-filter").value = "";
  }
  if (folder !== state.folder) {
    state.filter = "";
    $("prefix-filter").value = "";
  }
  if (folder !== state.folder || state.shownBucket !== name) setTableState("Loading…");
  state.folder = folder;
  state.shownBucket = name;

  showView("view-bucket");
  document.title = `${name} · s3bender`;
  setCrumbs([{ label: "Buckets", href: "#/" }, { label: name }]);
  $("bucket-title").textContent = name;
  showBucketMeta(name);
  if (!state.buckets.length && store.adminKey.get()) {
    api("/admin/buckets", { headers: adminHeaders() })
      .then((list) => { state.buckets = list; if (state.bucket?.name === name) showBucketMeta(name); })
      .catch(() => { /* description is cosmetic */ });
  }
  renderPathCrumbs();
  $("drop-target").textContent = `${name}/${folder}`;
  await listObjects();
}

function showBucketMeta(name) {
  const meta = state.buckets.find((b) => b.name === name);
  $("bucket-desc").textContent = meta?.description || (meta ? `Created ${formatDate(meta.createdAt)}` : "Signed with this bucket's own keys");
}

function renderPathCrumbs() {
  const name = state.bucket.name;
  const segs = state.folder.split("/").filter(Boolean);
  const parts = [segs.length ? `<a href="${bucketHref(name)}">${escapeHtml(name)}</a>` : `<span class="here">${escapeHtml(name)}</span>`];
  segs.forEach((seg, i) => {
    const folder = segs.slice(0, i + 1).join("/") + "/";
    parts.push(i === segs.length - 1 ? `<span class="here">${escapeHtml(seg)}</span>` : `<a href="${bucketHref(name, folder)}">${escapeHtml(seg)}</a>`);
  });
  $("path-crumbs").innerHTML = parts.join('<span class="sep">/</span>') + '<span class="sep">/</span>';
}

/** Reset to the first page and refresh both the page and the summary. */
async function listObjects() {
  state.pageCursors = [null];
  state.pageIndex = 0;
  await Promise.all([fetchObjectPage(), refreshStats()]);
}

function setTableState(html) {
  document.querySelector("#objects-table tbody").innerHTML = `<tr><td class="state" colspan="5">${html}</td></tr>`;
}

async function fetchObjectPage() {
  const seq = ++state.listSeq;
  const prefix = listPrefix();
  const cursor = state.pageCursors[state.pageIndex] || null;
  const params = new URLSearchParams({ limit: String(state.pageSize) });
  if (prefix) params.set("prefix", prefix);
  if (cursor) params.set("cursor", cursor);
  const rawPath = `/buckets/${state.bucket.name}/objects`;
  if (!document.querySelector("#objects-table tbody tr")) setTableState("Loading…");
  try {
    const resp = await api(`/buckets/${encodeURIComponent(state.bucket.name)}/objects?${params}`, {
      headers: await bucketAuthHeaders("GET", rawPath),
    });
    if (seq !== state.listSeq) return;
    if (resp.isTruncated && state.pageCursors[state.pageIndex + 1] === undefined) {
      state.pageCursors[state.pageIndex + 1] = resp.nextCursor;
    }
    renderObjectsPage(resp);
  } catch (err) {
    if (seq !== state.listSeq) return;
    $("objects-pager").hidden = true;
    if (err.status === 401 || err.status === 403) {
      setTableState(`<strong>These keys don't work for this bucket</strong>${escapeHtml(err.message)}`);
      toast("Bucket keys were rejected - update them with the Keys button", true);
    } else {
      setTableState(`<strong>Couldn't list objects</strong>${escapeHtml(err.message)}`);
    }
  }
}

/**
 * Whole-bucket (or whole-prefix) totals, computed server-side from the object index. The
 * folder/file split is S3-style with delimiter="/" over the *full* key, so it's only meaningful
 * at the bucket root - inside a folder only the object count and size are shown.
 */
async function refreshStats() {
  const prefix = listPrefix();
  const rawPath = `/buckets/${state.bucket.name}/stats`;
  const query = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
  try {
    const s = await api(`/buckets/${encodeURIComponent(state.bucket.name)}/stats${query}`, {
      headers: await bucketAuthHeaders("GET", rawPath),
    });
    state.totalObjects = s.objects;
    $("stat-objects").textContent = s.objects.toLocaleString();
    $("stat-size").textContent = formatBytes(s.totalBytes);
    $("stat-folders").textContent = s.topLevelFolders.toLocaleString();
    $("stat-files").textContent = s.topLevelFiles.toLocaleString();
    for (const el of document.querySelectorAll(".root-only")) el.hidden = !!prefix;
    $("objects-summary").hidden = false;
    updatePagerInfo();
  } catch {
    $("objects-summary").hidden = true;
  }
}

function renderObjectsPage(resp) {
  const tbody = document.querySelector("#objects-table tbody");
  tbody.innerHTML = "";
  state.lastPageCount = resp.objects.length;

  if (resp.objects.length === 0 && state.pageIndex === 0) {
    $("objects-pager").hidden = true;
    setTableState(state.filter
      ? `<strong>No matches</strong>Nothing in this folder starts with “${escapeHtml(state.filter)}”.`
      : `<strong>${state.folder ? "This folder is empty" : "This bucket is empty"}</strong>Drop files anywhere here, or use Upload.`);
    return;
  }

  for (const o of resp.objects) {
    const kind = fileKind(o.contentType);
    const rel = o.key.startsWith(state.folder) ? o.key.slice(state.folder.length) : o.key;
    const segs = rel.split("/");
    const fileName = segs.pop();
    let folderAcc = state.folder;
    const folderLinks = segs
      .map((s) => {
        folderAcc += s + "/";
        return `<a class="seg" href="${bucketHref(state.bucket.name, folderAcc)}">${escapeHtml(s)}/</a>`;
      })
      .join("");
    const previewable = isPreviewable(o.contentType);

    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>
        <div class="name-cell">
          <span class="ficon ${kind}">${icon(kind)}</span>
          <span class="key" title="${escapeHtml(o.key)}">${folderLinks}<button class="file" type="button">${escapeHtml(fileName)}</button></span>
        </div>
      </td>
      <td class="r muted hide-xs">${formatBytes(o.size)}</td>
      <td class="muted hide-sm" title="${escapeHtml(new Date(o.lastModified).toLocaleString())}">${escapeHtml(formatDate(o.lastModified))}</td>
      <td class="hide-sm"><span class="badge ${o.public ? "public" : ""}">${o.public ? "public" : "private"}</span></td>
      <td class="actions">
        <div class="btn-row">
          ${previewable ? `<button class="icon-btn" type="button" data-act="view" title="Preview" aria-label="Preview">${icon("eye")}</button>` : ""}
          <button class="icon-btn" type="button" data-act="download" title="Download" aria-label="Download">${icon("download")}</button>
          <button class="icon-btn" type="button" data-act="more" title="More" aria-label="More actions">${icon("more")}</button>
        </div>
      </td>`;

    const view = () => previewObject(o);
    tr.querySelector(".file").addEventListener("click", () => (previewable ? view() : downloadObject(o.key)));
    tr.querySelector('[data-act="view"]')?.addEventListener("click", view);
    tr.querySelector('[data-act="download"]').addEventListener("click", () => downloadObject(o.key));
    tr.querySelector('[data-act="more"]').addEventListener("click", (e) => {
      const publicUrl = `${location.origin}${objectPaths("objects", o.key).url}`;
      openMenu(e.currentTarget, [
        ...(previewable ? [{ label: "Preview", action: view }] : []),
        { label: `Download (${formatBytes(o.size)})`, action: () => downloadObject(o.key) },
        "sep",
        o.public
          ? { label: "Copy public URL", action: () => copyText(publicUrl, "Public URL copied") }
          : { label: "Copy presigned link (1 h)", action: () => presignObject(o.key) },
        { label: "Copy ETag", action: () => copyText(o.eTag, "ETag copied") },
        { label: "Copy key", action: () => copyText(o.key, "Key copied") },
        "sep",
        o.public
          ? { label: "Make private", action: () => setObjectAcl(o.key, false) }
          : { label: "Make public", action: () => setObjectAcl(o.key, true) },
        "sep",
        { label: "Delete…", danger: true, action: () => deleteObject(o.key) },
      ]);
    });
    tbody.appendChild(tr);
  }

  $("pager-pos").textContent = `Page ${state.pageIndex + 1}`;
  $("page-prev").disabled = state.pageIndex <= 0;
  $("page-next").disabled = !resp.isTruncated;
  $("objects-pager").hidden = false;
  updatePagerInfo();
}

function updatePagerInfo() {
  const count = state.lastPageCount || 0;
  if (!count) return;
  const start = state.pageIndex * state.pageSize;
  $("pager-info").textContent = state.totalObjects
    ? `${(start + 1).toLocaleString()}–${(start + count).toLocaleString()} of ${state.totalObjects.toLocaleString()}`
    : `${start + 1}–${start + count}`;
}

// ---- object actions ----

/**
 * Uploads go one at a time through XHR (fetch can't report upload progress) into the current
 * folder. Each request is signed just before it's sent.
 */
async function uploadFiles(fileList) {
  const files = Array.from(fileList);
  if (!files.length || !state.bucket) return;
  const makePublic = $("upload-public").checked;
  const tray = $("upload-tray");
  const list = $("tray-list");
  clearTimeout(uploadFiles.hideTimer);
  for (const li of list.querySelectorAll("li")) {
    if (li.querySelector(".status.done")) li.remove();
  }
  tray.hidden = false;

  const items = files.map((file) => {
    const li = document.createElement("li");
    li.innerHTML = `<div class="row"><span class="fname"></span><span class="status">queued</span></div><div class="bar"><span></span></div>`;
    li.querySelector(".fname").textContent = state.folder + file.name;
    list.prepend(li);
    return { file, li };
  });

  let ok = 0;
  for (const [i, { file, li }] of items.entries()) {
    $("tray-title").textContent = `Uploading ${i + 1} of ${items.length}`;
    try {
      await uploadOne(file, state.folder + file.name, makePublic, li);
      ok++;
    } catch (err) {
      setItem(li, "err", err.message, 100);
    }
  }
  $("tray-title").textContent = ok === items.length ? `Uploaded ${ok} file${ok === 1 ? "" : "s"}` : `${ok} of ${items.length} uploaded`;
  if (ok === items.length && !list.querySelector(".status.err")) {
    uploadFiles.hideTimer = setTimeout(() => { tray.hidden = true; list.innerHTML = ""; }, 4000);
  }
  await listObjects();
}

function setItem(li, cls, text, pct) {
  const status = li.querySelector(".status");
  status.className = `status ${cls}`;
  status.textContent = text;
  const bar = li.querySelector(".bar");
  bar.className = `bar ${cls}`;
  bar.firstElementChild.style.width = `${pct}%`;
}

async function uploadOne(file, key, makePublic, li) {
  const { raw, url } = objectPaths("objects", key);
  const headers = await bucketAuthHeaders("PUT", raw);
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    xhr.setRequestHeader("Authorization", headers.Authorization);
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    if (makePublic) xhr.setRequestHeader("X-S3Bender-Public", "true");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) {
        const pct = Math.round((e.loaded / e.total) * 100);
        setItem(li, "", `${pct}%`, pct);
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        setItem(li, "done", formatBytes(file.size), 100);
        resolve();
      } else {
        let msg = `HTTP ${xhr.status}`;
        try { msg = JSON.parse(xhr.responseText).message || msg; } catch { /* not JSON */ }
        reject(new Error(msg));
      }
    };
    xhr.onerror = () => reject(new Error("network error"));
    xhr.send(file);
  });
}

/**
 * Flips an existing object's visibility via PUT /buckets/{bucket}/acl/{key} - no re-upload.
 * Public objects are readable at their plain URL with no credential; private is the default.
 */
async function setObjectAcl(key, makePublic) {
  const { raw, url } = objectPaths("acl", key);
  try {
    await api(url, {
      method: "PUT",
      headers: { ...(await bucketAuthHeaders("PUT", raw)), "Content-Type": "application/json" },
      body: JSON.stringify({ public: makePublic }),
    });
    toast(`"${key}" is now ${makePublic ? "public" : "private"}`);
    await fetchObjectPage();
  } catch (err) {
    toast(`Failed to change visibility: ${err.message}`, true);
  }
}

async function deleteObject(key) {
  if (!confirm(`Delete "${key}"?`)) return;
  const { raw, url } = objectPaths("objects", key);
  try {
    await api(url, { method: "DELETE", headers: await bucketAuthHeaders("DELETE", raw) });
    toast(`Deleted "${key}"`);
    await listObjects();
  } catch (err) {
    toast(`Delete failed: ${err.message}`, true);
  }
}

async function requestPresign(bucket, key, expiresInSeconds = 300) {
  const rawPath = `/buckets/${bucket}/presign`;
  return api(`/buckets/${encodeURIComponent(bucket)}/presign`, {
    method: "POST",
    headers: { ...(await bucketAuthHeaders("POST", rawPath)), "Content-Type": "application/json" },
    body: JSON.stringify({ key, method: "GET", expiresInSeconds }),
  });
}

/**
 * The signature covers only the path, so a presigned URL is valid on any host that serves this
 * API. Re-rooting it on this page's origin keeps previews and downloads same-origin (the
 * `download` attribute only works same-origin) whatever S3BENDER_PUBLIC_BASE_URL says.
 */
function sameOrigin(url) {
  const u = new URL(url, location.href);
  return location.origin + u.pathname + u.search;
}

async function presignObject(key) {
  try {
    const result = await requestPresign(state.bucket.name, key, 3600);
    await copyText(result.url, `Presigned link copied (expires ${new Date(result.expiresAt).toLocaleTimeString()})`);
  } catch (err) {
    toast(`Presign failed: ${err.message}`, true);
  }
}

/**
 * Downloads stream straight to disk through a short-lived presigned URL instead of being read
 * into memory with fetch() first - large objects start immediately.
 */
async function downloadObject(key) {
  try {
    const result = await requestPresign(state.bucket.name, key, 300);
    const a = document.createElement("a");
    a.href = sameOrigin(result.url);
    a.download = key.split("/").pop();
    document.body.appendChild(a);
    a.click();
    a.remove();
  } catch (err) {
    toast(`Download failed: ${err.message}`, true);
  }
}

// Preview URLs are reused per object *version* (bucket + key + ETag, under the current access key -
// a rotation invalidates every URL signed with the old one) while they have at least a
// minute left. Same URL means the browser can answer a repeat preview from its cache (the server
// marks presigned responses cacheable, revalidating with a body-less 304 after 5 minutes) instead of
// re-downloading the whole object; a re-upload changes the ETag and so naturally gets a fresh URL.
const previewUrls = new Map();
const PREVIEW_URL_SECONDS = 900;

async function previewUrl(bucket, key, etag) {
  const cacheKey = `${state.bucket.accessKey}\n${bucket}\n${key}\n${etag}`;
  const cached = previewUrls.get(cacheKey);
  if (cached && new Date(cached.expiresAt).getTime() - Date.now() > 60_000) return cached;
  const result = await requestPresign(bucket, key, PREVIEW_URL_SECONDS);
  previewUrls.set(cacheKey, result);
  return result;
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
async function previewObject(o) {
  try {
    const result = await previewUrl(state.bucket.name, o.key, o.eTag);
    const src = sameOrigin(result.url);
    $("preview-title").textContent = o.key;
    $("preview-download").onclick = () => downloadObject(o.key);
    const body = $("preview-body");
    body.innerHTML = "";

    let el;
    const ct = o.contentType;
    if (ct.startsWith("image/")) {
      el = document.createElement("img");
      el.alt = o.key;
    } else if (ct.startsWith("video/")) {
      el = document.createElement("video");
      el.controls = true;
      el.autoplay = true;
    } else if (ct.startsWith("audio/")) {
      el = document.createElement("audio");
      el.controls = true;
      el.autoplay = true;
    } else {
      // PDF and text render fine in an iframe.
      el = document.createElement("iframe");
    }
    el.src = src;
    body.appendChild(el);
    $("preview-dialog").showModal();
  } catch (err) {
    toast(`Preview failed: ${err.message}`, true);
  }
}

// ---- popover menu ----

function openMenu(anchor, items) {
  const menu = $("menu");
  menu.innerHTML = "";
  for (const item of items) {
    if (item === "sep") {
      menu.appendChild(document.createElement("hr"));
      continue;
    }
    const btn = document.createElement("button");
    btn.type = "button";
    btn.role = "menuitem";
    btn.textContent = item.label;
    if (item.danger) btn.className = "danger";
    btn.addEventListener("click", () => {
      closeMenu();
      item.action();
    });
    menu.appendChild(btn);
  }
  menu.hidden = false;
  const r = anchor.getBoundingClientRect();
  const m = menu.getBoundingClientRect();
  const top = r.bottom + 4 + m.height > window.innerHeight ? r.top - m.height - 4 : r.bottom + 4;
  menu.style.top = `${Math.max(8, top)}px`;
  menu.style.left = `${Math.max(8, Math.min(r.right - m.width, window.innerWidth - m.width - 8))}px`;
  menu.querySelector("button")?.focus();
}

function closeMenu() {
  $("menu").hidden = true;
}

document.addEventListener("click", (e) => {
  if (!$("menu").hidden && !e.target.closest("#menu") && !e.target.closest("[data-menu], [data-act='more']")) closeMenu();
});
document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeMenu(); });
window.addEventListener("resize", closeMenu);
window.addEventListener("scroll", closeMenu, true);

// ---- wire up ----

$("admin-chip").addEventListener("click", () => {
  $("admin-key").value = store.adminKey.get();
  $("settings-dialog").showModal();
});
$("settings-dialog").addEventListener("close", () => {
  if ($("settings-dialog").returnValue === "save") setAdminKey($("admin-key").value);
  $("settings-dialog").returnValue = "";
});
$("admin-forget").addEventListener("click", () => {
  $("settings-dialog").close();
  setAdminKey("");
});
$("admin-gate").addEventListener("submit", (e) => {
  e.preventDefault();
  setAdminKey($("gate-admin-key").value);
  $("gate-admin-key").value = "";
});

$("new-bucket").addEventListener("click", () => {
  $("create-form").reset();
  $("create-dialog").showModal();
});
$("create-dialog").addEventListener("close", () => {
  if ($("create-dialog").returnValue === "create") {
    createBucket($("new-bucket-name").value.trim(), $("new-bucket-description").value.trim(), $("create-remember").checked);
  }
  $("create-dialog").returnValue = "";
});

$("open-with-keys").addEventListener("click", async () => {
  const creds = await promptKeys({ title: "Open a bucket with its keys" });
  if (!creds) return;
  state.bucket = creds;
  location.hash = bucketHref(creds.name);
  if (parseRoute().bucket === creds.name) render();
});

$("bucket-keys").addEventListener("click", async () => {
  const name = state.bucket.name;
  const creds = await promptKeys({ name, lockName: true, title: `Keys for ${name}`, action: "Save" });
  if (!creds) return;
  state.bucket = { ...creds, name };
  await listObjects();
});

$("refresh-objects").addEventListener("click", listObjects);
$("upload-btn").addEventListener("click", () => $("file-input").click());
$("file-input").addEventListener("change", (e) => {
  uploadFiles(e.target.files);
  e.target.value = "";
});
$("tray-close").addEventListener("click", () => {
  $("upload-tray").hidden = true;
  $("tray-list").innerHTML = "";
});

let filterTimer;
$("prefix-filter").addEventListener("input", (e) => {
  clearTimeout(filterTimer);
  filterTimer = setTimeout(() => {
    state.filter = e.target.value;
    listObjects();
  }, 250);
});

$("page-prev").addEventListener("click", () => {
  if (state.pageIndex > 0) {
    state.pageIndex -= 1;
    fetchObjectPage();
  }
});
$("page-next").addEventListener("click", () => {
  state.pageIndex += 1;
  fetchObjectPage();
});
$("page-size").addEventListener("change", (e) => {
  state.pageSize = parseInt(e.target.value, 10) || 25;
  listObjects();
});

// Drag-and-drop anywhere over the bucket view uploads into the current folder.
let dragDepth = 0;
const view = $("view-bucket");
const hasFiles = (e) => Array.from(e.dataTransfer?.types || []).includes("Files");
view.addEventListener("dragenter", (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth++;
  $("drop-overlay").hidden = false;
});
view.addEventListener("dragover", (e) => { if (hasFiles(e)) e.preventDefault(); });
view.addEventListener("dragleave", () => {
  if (--dragDepth <= 0) {
    dragDepth = 0;
    $("drop-overlay").hidden = true;
  }
});
view.addEventListener("drop", (e) => {
  if (!hasFiles(e)) return;
  e.preventDefault();
  dragDepth = 0;
  $("drop-overlay").hidden = true;
  uploadFiles(e.dataTransfer.files);
});

for (const btn of document.querySelectorAll("[data-copy]")) {
  btn.addEventListener("click", () => copyText($(btn.dataset.copy).value, "Copied"));
}
$("dialog-close").addEventListener("click", () => $("secret-dialog").close());
$("preview-close").addEventListener("click", () => $("preview-dialog").close());
$("preview-dialog").addEventListener("close", () => ($("preview-body").innerHTML = "")); // stop playback

window.addEventListener("hashchange", render);


updateAdminChip();
render();
