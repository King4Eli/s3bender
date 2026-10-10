package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const (
	testAdminKey  = "test-admin-key"
	testMasterKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="
)

type harness struct {
	t   *testing.T
	srv *Server
	api *httptest.Server // plain API port
	ui  *httptest.Server // console port
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	srv, err := New(Config{
		StorageRoot:             filepath.Join(dir, "objects"),
		DBPath:                  filepath.Join(dir, "s3bender.db"),
		AdminAPIKey:             testAdminKey,
		MasterKey:               testMasterKey,
		ClockSkewSeconds:        900,
		MaxPresignExpirySeconds: 604800,
	}, fstest.MapFS{"index.html": {Data: []byte("<h1>console</h1>")}, "app.js": {Data: []byte("//js")}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, srv: srv, api: httptest.NewServer(srv.Handler(false)), ui: httptest.NewServer(srv.Handler(true))}
	t.Cleanup(func() { h.api.Close(); h.ui.Close(); srv.Close() })
	return h
}

type creds struct {
	Name      string `json:"name"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

func (h *harness) do(req *http.Request) *http.Response {
	h.t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}

func (h *harness) admin(method, path string) *http.Response {
	req, _ := http.NewRequest(method, h.api.URL+path, nil)
	req.Header.Set(adminKeyHeader, testAdminKey)
	return h.do(req)
}

func (h *harness) createBucket(name string) creds {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.api.URL+"/admin/buckets", strings.NewReader(`{"name":"`+name+`"}`))
	req.Header.Set(adminKeyHeader, testAdminKey)
	res := h.do(req)
	expectStatus(h.t, res, http.StatusCreated)
	var c creds
	decode(h.t, res, &c)
	return c
}

// signed builds a header-authenticated request. rawPath is the decoded path that gets signed;
// the URL uses the per-segment escaped form, as a real client would.
func (h *harness) signed(c creds, method, rawPath, query string, body io.Reader) *http.Request {
	segs := strings.Split(rawPath, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	req, _ := http.NewRequest(method, h.api.URL+strings.Join(segs, "/")+query, body)
	ts := time.Now().Unix()
	req.Header.Set("Authorization", "S3BENDER-HMAC-SHA256 AccessKey="+c.AccessKey+",Timestamp="+
		strconv.FormatInt(ts, 10)+",Signature="+Sign(c.SecretKey, StringToSign(method, rawPath, ts)))
	return req
}

func (h *harness) put(c creds, key, content string, headers ...string) {
	h.t.Helper()
	req := h.signed(c, "PUT", "/buckets/"+c.Name+"/objects/"+key, "", strings.NewReader(content))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	expectStatus(h.t, h.do(req), http.StatusOK)
}

func (h *harness) list(c creds, query string) ListObjectsResponse {
	h.t.Helper()
	res := h.do(h.signed(c, "GET", "/buckets/"+c.Name+"/objects", query, nil))
	expectStatus(h.t, res, http.StatusOK)
	var out ListObjectsResponse
	decode(h.t, res, &out)
	return out
}

func (h *harness) stats(c creds, query string) BucketStats {
	h.t.Helper()
	res := h.do(h.signed(c, "GET", "/buckets/"+c.Name+"/stats", query, nil))
	expectStatus(h.t, res, http.StatusOK)
	var out BucketStats
	decode(h.t, res, &out)
	return out
}

func (h *harness) presign(c creds, key string, expires int) string {
	h.t.Helper()
	body := `{"key":"` + key + `","method":"GET","expiresInSeconds":` + strconv.Itoa(expires) + `}`
	res := h.do(h.signed(c, "POST", "/buckets/"+c.Name+"/presign", "", strings.NewReader(body)))
	expectStatus(h.t, res, http.StatusOK)
	var out struct{ URL string }
	decode(h.t, res, &out)
	u, _ := url.Parse(out.URL)
	return h.api.URL + u.RequestURI()
}

func (h *harness) clearIndexRows() {
	if _, err := h.srv.store.db.Exec(`DELETE FROM "Objects"`); err != nil {
		h.t.Fatal(err)
	}
}

func expectStatus(t *testing.T, res *http.Response, want int) {
	t.Helper()
	if res.StatusCode != want {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("%s %s: status %d, want %d (body %s)", res.Request.Method, res.Request.URL.Path, res.StatusCode, want, body)
	}
}

func decode(t *testing.T, res *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func keys(r ListObjectsResponse) []string {
	var out []string
	for _, o := range r.Objects {
		out = append(out, o.Key)
	}
	return out
}

// ---- end-to-end flows (ported from the C# BucketFlowTests) ----

func TestCreateUploadDownloadAndPresign(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("flow-test-bucket")
	path := "/buckets/" + c.Name + "/objects/docs/hello.txt"

	h.put(c, "docs/hello.txt", "hello s3bender", "Content-Type", "text/plain; charset=utf-8")

	res := h.do(h.signed(c, "GET", path, "", nil))
	expectStatus(t, res, http.StatusOK)
	if got := readBody(t, res); got != "hello s3bender" {
		t.Fatalf("body = %q", got)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type not replayed: %q", ct)
	}
	if res.Header.Get("X-S3Bender-Public") != "false" {
		t.Fatal("objects must default to private")
	}

	// Presigned URL works with no Authorization header at all.
	res = h.do(must(http.NewRequest("GET", h.presign(c, "docs/hello.txt", 60), nil)))
	expectStatus(t, res, http.StatusOK)
	if got := readBody(t, res); got != "hello s3bender" {
		t.Fatalf("presigned body = %q", got)
	}

	// Unauthenticated access to a private object is refused.
	expectStatus(t, h.do(must(http.NewRequest("GET", h.api.URL+path, nil))), http.StatusUnauthorized)
}

func TestRotateKeyInvalidatesOldCredentialsAndKeepsObjects(t *testing.T) {
	h := newHarness(t)
	orig := h.createBucket("rotate-test-bucket")
	h.put(orig, "keep-me.txt", "still here")
	presigned := h.presign(orig, "keep-me.txt", 60)

	res := h.admin("POST", "/admin/buckets/rotate-test-bucket/rotate")
	expectStatus(t, res, http.StatusOK)
	var rotated creds
	decode(t, res, &rotated)
	if rotated.AccessKey == orig.AccessKey || rotated.SecretKey == orig.SecretKey {
		t.Fatal("rotation must issue a new pair")
	}

	path := "/buckets/" + orig.Name + "/objects/keep-me.txt"
	expectStatus(t, h.do(h.signed(orig, "GET", path, "", nil)), http.StatusUnauthorized)
	expectStatus(t, h.do(must(http.NewRequest("GET", presigned, nil))), http.StatusUnauthorized)

	res = h.do(h.signed(rotated, "GET", path, "", nil))
	expectStatus(t, res, http.StatusOK)
	if got := readBody(t, res); got != "still here" {
		t.Fatalf("body = %q", got)
	}
}

func TestCredentialForOneBucketRejectedOnAnother(t *testing.T) {
	h := newHarness(t)
	a := h.createBucket("bucket-a")
	h.createBucket("bucket-b")
	// Mathematically valid signature, wrong bucket.
	res := h.do(h.signed(a, "GET", "/buckets/bucket-b/objects", "", nil))
	expectStatus(t, res, http.StatusUnauthorized)
}

func TestBadSignatureAndAdminKey(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("sig-test")
	bad := c
	bad.SecretKey = "wrong"
	expectStatus(t, h.do(h.signed(bad, "GET", "/buckets/sig-test/objects", "", nil)), http.StatusForbidden)

	req, _ := http.NewRequest("GET", h.api.URL+"/admin/buckets", nil)
	req.Header.Set(adminKeyHeader, "nope")
	expectStatus(t, h.do(req), http.StatusUnauthorized)
	expectStatus(t, h.admin("GET", "/admin/buckets"), http.StatusOK)
}

func TestBucketValidationAndConflicts(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{`{"name":"Bad_Name"}`, `{}`, `not json`, ``} {
		req, _ := http.NewRequest("POST", h.api.URL+"/admin/buckets", strings.NewReader(body))
		req.Header.Set(adminKeyHeader, testAdminKey)
		res := h.do(req)
		expectStatus(t, res, http.StatusBadRequest)
		var e errorResponse
		decode(t, res, &e)
		if e.Code != "InvalidRequest" {
			t.Fatalf("body %q: code %q", body, e.Code)
		}
	}
	c := h.createBucket("dupe")
	req, _ := http.NewRequest("POST", h.api.URL+"/admin/buckets", strings.NewReader(`{"name":"dupe"}`))
	req.Header.Set(adminKeyHeader, testAdminKey)
	expectStatus(t, h.do(req), http.StatusConflict)

	h.put(c, "x", "1")
	expectStatus(t, h.admin("DELETE", "/admin/buckets/dupe"), http.StatusConflict)
	expectStatus(t, h.do(h.signed(c, "DELETE", "/buckets/dupe/objects/x", "", nil)), http.StatusNoContent)
	expectStatus(t, h.admin("DELETE", "/admin/buckets/dupe"), http.StatusNoContent)
	expectStatus(t, h.admin("DELETE", "/admin/buckets/dupe"), http.StatusNotFound)
}

func TestTraversalKeysRejected(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("traversal")
	for _, key := range []string{"../escape", "a/../../b", "a/..", " "} {
		res := h.do(h.signed(c, "PUT", "/buckets/traversal/objects/"+key, "", strings.NewReader("x")))
		expectStatus(t, res, http.StatusBadRequest)
		var e errorResponse
		decode(t, res, &e)
		if e.Code != "InvalidKey" {
			t.Fatalf("key %q: code %q", key, e.Code)
		}
	}
}

func TestPublicObjectsSkipAuthAndACLFlips(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("public-test")
	h.put(c, "pub.png", "PNGDATA", "Content-Type", "image/png", "X-S3Bender-Public", "true")

	plain := h.api.URL + "/buckets/public-test/objects/pub.png"
	res := h.do(must(http.NewRequest("GET", plain, nil)))
	expectStatus(t, res, http.StatusOK)
	if res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-S3Bender-Public") != "true" {
		t.Fatalf("headers %v", res.Header)
	}
	// Only GET/HEAD bypass auth - a public object still can't be deleted anonymously.
	expectStatus(t, h.do(must(http.NewRequest("DELETE", plain, nil))), http.StatusUnauthorized)

	res = h.do(h.signed(c, "PUT", "/buckets/public-test/acl/pub.png", "", strings.NewReader(`{"public":false}`)))
	expectStatus(t, res, http.StatusOK)
	expectStatus(t, h.do(must(http.NewRequest("GET", plain, nil))), http.StatusUnauthorized)

	// Re-uploading without the header resets to private.
	h.put(c, "pub.png", "v2", "X-S3Bender-Public", "true")
	h.put(c, "pub.png", "v3")
	expectStatus(t, h.do(must(http.NewRequest("GET", plain, nil))), http.StatusUnauthorized)
}

// ---- the fast read path: Range, conditional requests, cache headers ----

func TestRangeConditionalAndCacheHeaders(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("perf-test")
	content := strings.Repeat("0123456789", 1000)
	h.put(c, "video.mp4", content, "Content-Type", "video/mp4")
	path := "/buckets/perf-test/objects/video.mp4"

	// Range → 206 with just the requested bytes, so <video> can seek and start immediately.
	req := h.signed(c, "GET", path, "", nil)
	req.Header.Set("Range", "bytes=10-19")
	res := h.do(req)
	expectStatus(t, res, http.StatusPartialContent)
	if got := readBody(t, res); got != "0123456789" {
		t.Fatalf("range body = %q", got)
	}
	if cr := res.Header.Get("Content-Range"); cr != "bytes 10-19/10000" {
		t.Fatalf("Content-Range = %q", cr)
	}
	if res.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatal("Accept-Ranges missing")
	}

	// Full GET carries an ETag; replaying it gets a body-less 304.
	res = h.do(h.signed(c, "GET", path, "", nil))
	expectStatus(t, res, http.StatusOK)
	etag := res.Header.Get("ETag")
	if res.Header.Get("Cache-Control") != "private, no-cache" || res.Header.Get("Content-Length") != "10000" {
		t.Fatalf("headers %v", res.Header)
	}
	req = h.signed(c, "GET", path, "", nil)
	req.Header.Set("If-None-Match", etag)
	res = h.do(req)
	expectStatus(t, res, http.StatusNotModified)
	if readBody(t, res) != "" {
		t.Fatal("304 must have no body")
	}

	// Presigned responses are cacheable for (at most) the URL's remaining lifetime.
	res = h.do(must(http.NewRequest("GET", h.presign(c, "video.mp4", 120), nil)))
	expectStatus(t, res, http.StatusOK)
	cc := res.Header.Get("Cache-Control")
	if !strings.HasPrefix(cc, "private, max-age=") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if age, _ := strconv.Atoi(strings.TrimPrefix(cc, "private, max-age=")); age < 115 || age > 120 {
		t.Fatalf("max-age %d not bounded by presign lifetime", age)
	}

	// HEAD: same metadata, no body.
	res = h.do(h.signed(c, "HEAD", path, "", nil))
	expectStatus(t, res, http.StatusOK)
	if res.Header.Get("Content-Length") != "10000" || res.Header.Get("ETag") != etag {
		t.Fatalf("HEAD headers %v", res.Header)
	}
}

func TestConsoleAssetsOnlyOnFrontendPort(t *testing.T) {
	h := newHarness(t)
	res := h.do(must(http.NewRequest("GET", h.ui.URL+"/", nil)))
	expectStatus(t, res, http.StatusOK)
	if !strings.Contains(readBody(t, res), "console") {
		t.Fatal("console index not served")
	}
	expectStatus(t, h.do(must(http.NewRequest("GET", h.ui.URL+"/index.html", nil))), http.StatusOK)
	expectStatus(t, h.do(must(http.NewRequest("GET", h.api.URL+"/", nil))), http.StatusNotFound)
	expectStatus(t, h.do(must(http.NewRequest("GET", h.api.URL+"/app.js", nil))), http.StatusNotFound)
	// The API itself answers on both ports.
	expectStatus(t, h.do(must(http.NewRequest("GET", h.ui.URL+"/healthz", nil))), http.StatusOK)
	expectStatus(t, h.do(must(http.NewRequest("GET", h.api.URL+"/healthz", nil))), http.StatusOK)
}

// ---- object index (ported from the C# ObjectIndexTests) ----

func TestListPaginatesByCursorInKeyOrder(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("page-test")
	for _, k := range []string{"k4", "k0", "k2", "k1", "k3"} {
		h.put(c, "items/"+k, "x")
	}
	p1 := h.list(c, "?prefix=items/&limit=2")
	if !slices.Equal(keys(p1), []string{"items/k0", "items/k1"}) || !p1.IsTruncated || *p1.NextCursor != "items/k1" {
		t.Fatalf("page 1: %+v", p1)
	}
	p2 := h.list(c, "?prefix=items/&limit=2&cursor="+url.QueryEscape(*p1.NextCursor))
	if !slices.Equal(keys(p2), []string{"items/k2", "items/k3"}) || !p2.IsTruncated {
		t.Fatalf("page 2: %+v", p2)
	}
	p3 := h.list(c, "?prefix=items/&limit=2&cursor="+url.QueryEscape(*p2.NextCursor))
	if !slices.Equal(keys(p3), []string{"items/k4"}) || p3.IsTruncated || p3.NextCursor != nil {
		t.Fatalf("page 3: %+v", p3)
	}
}

func TestListFiltersByPrefixAndClampsLimit(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("prefix-test")
	h.put(c, "a/one", "x")
	h.put(c, "a/two", "x")
	h.put(c, "b/one", "x")
	h.put(c, "A/upper", "x") // prefix matching is case-sensitive

	if got := keys(h.list(c, "?prefix=a/")); !slices.Equal(got, []string{"a/one", "a/two"}) {
		t.Fatalf("prefix a/: %v", got)
	}
	low := h.list(c, "?limit=0")
	if len(low.Objects) != 1 || !low.IsTruncated {
		t.Fatalf("limit=0: %+v", low)
	}
	high := h.list(c, "?limit=999999")
	if len(high.Objects) != 4 || high.IsTruncated {
		t.Fatalf("limit=999999: %+v", high)
	}
}

func TestStatsAggregateObjectsBytesAndTopLevelSplit(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("stats-test")
	h.put(c, "docs/a.txt", "hello world")
	h.put(c, "docs/b.txt", "abc")
	h.put(c, "readme.txt", "hi")

	if s := h.stats(c, ""); s != (BucketStats{3, 16, 1, 1}) {
		t.Fatalf("stats: %+v", s)
	}
	if s := h.stats(c, "?prefix=docs/"); s != (BucketStats{2, 14, 1, 0}) {
		t.Fatalf("scoped stats: %+v", s)
	}
}

func TestDeleteRemovesObjectFromIndex(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("delete-test")
	h.put(c, "keep.txt", "x")
	h.put(c, "gone.txt", "x")
	expectStatus(t, h.do(h.signed(c, "DELETE", "/buckets/delete-test/objects/gone.txt", "", nil)), http.StatusNoContent)
	// Idempotent.
	expectStatus(t, h.do(h.signed(c, "DELETE", "/buckets/delete-test/objects/gone.txt", "", nil)), http.StatusNoContent)

	if got := keys(h.list(c, "")); !slices.Equal(got, []string{"keep.txt"}) {
		t.Fatalf("list: %v", got)
	}
	if h.stats(c, "").Objects != 1 {
		t.Fatal("stats not updated")
	}
}

func TestAdminReindexRebuildsIndexFromDisk(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("reindex-test")
	h.put(c, "one", "x")
	h.put(c, "nested/two", "yy")
	h.put(c, "nested/three", "zzz")
	h.clearIndexRows()

	res := h.admin("POST", "/admin/buckets/reindex-test/reindex")
	expectStatus(t, res, http.StatusOK)
	var body struct{ Indexed int }
	decode(t, res, &body)
	if body.Indexed != 3 {
		t.Fatalf("indexed = %d", body.Indexed)
	}
	if got := keys(h.list(c, "")); !slices.Equal(got, []string{"nested/three", "nested/two", "one"}) {
		t.Fatalf("list: %v", got)
	}
	if h.stats(c, "").TotalBytes != 6 {
		t.Fatal("bytes not reindexed")
	}
}

func TestFirstListSelfHealsWhenIndexRowsAreMissing(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("selfheal-test")
	h.put(c, "alpha", "x")
	h.put(c, "beta", "x")
	h.clearIndexRows()

	if got := keys(h.list(c, "")); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("list: %v", got)
	}
}

func TestLegacySidecarReadAsPrivateContentType(t *testing.T) {
	h := newHarness(t)
	c := h.createBucket("legacy-test")
	h.put(c, "old.txt", "x")
	h.clearIndexRows()
	// Pre-JSON sidecar format: just the Content-Type string.
	meta := h.srv.storage.metaPath("legacy-test", "old.txt")
	if err := writeFile(meta, "text/csv"); err != nil {
		t.Fatal(err)
	}
	res := h.do(h.signed(c, "GET", "/buckets/legacy-test/objects/old.txt", "", nil))
	expectStatus(t, res, http.StatusOK)
	if res.Header.Get("Content-Type") != "text/csv" || res.Header.Get("X-S3Bender-Public") != "false" {
		t.Fatalf("headers %v", res.Header)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
