package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// startTime is the Last-Modified of the embedded console assets, which only change on a new build.
var startTime = time.Now()

// presignCacheCap bounds how long a browser may reuse a presigned GET without revalidating - after
// that it sends If-None-Match and gets a body-less 304 if the object is unchanged.
const presignCacheCap = 5 * time.Minute

func (s *Server) health(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	return nil
}

// ---- admin ----

var bucketNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`)

type bucketCredentials struct {
	Name        string    `json:"name"`
	AccessKey   string    `json:"accessKey"`
	SecretKey   string    `json:"secretKey"`
	CreatedAt   time.Time `json:"createdAt"`
	Description *string   `json:"description"`
}

type bucketSummary struct {
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	Description *string   `json:"description"`
}

func (s *Server) createBucket(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	switch {
	case req.Name == "":
		return badRequest("InvalidRequest", "The Name field is required.")
	case !bucketNameRe.MatchString(req.Name):
		return badRequest("InvalidRequest", "must be 3-63 chars, lowercase alphanumeric and hyphens, not starting/ending with a hyphen")
	case req.Description != nil && utf8.RuneCountInString(*req.Description) > 200:
		return badRequest("InvalidRequest", "must be 200 characters or fewer")
	}

	ctx := r.Context()
	if existing, err := s.store.GetBucket(ctx, req.Name); err != nil {
		return err
	} else if existing != nil {
		return conflict("BucketAlreadyExists", "Bucket '%s' already exists", req.Name)
	}

	var description *string
	if req.Description != nil && strings.TrimSpace(*req.Description) != "" {
		d := strings.TrimSpace(*req.Description)
		description = &d
	}
	accessKey, secretKey := GenerateAccessKey(), GenerateSecretKey()
	b := &Bucket{
		Name: req.Name, Description: description, AccessKey: accessKey,
		EncryptedSecretKey: s.crypto.EncryptSecret(secretKey), CreatedAt: time.Now().UTC(),
	}
	if err := s.store.InsertBucket(ctx, b); err != nil {
		return err
	}
	if err := s.storage.CreateBucketDirectory(b.Name); err != nil {
		_ = s.store.DeleteBucket(ctx, b.Name)
		return err
	}
	writeJSON(w, http.StatusCreated, bucketCredentials{b.Name, accessKey, secretKey, b.CreatedAt, description})
	return nil
}

func (s *Server) listBuckets(w http.ResponseWriter, r *http.Request) error {
	buckets, err := s.store.ListBuckets(r.Context())
	if err != nil {
		return err
	}
	out := make([]bucketSummary, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, bucketSummary{b.Name, b.CreatedAt, b.Description})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) requireBucket(r *http.Request, name string) (*Bucket, error) {
	b, err := s.store.GetBucket(r.Context(), name)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, noSuchBucket(name)
	}
	return b, nil
}

func (s *Server) deleteBucket(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if _, err := s.requireBucket(r, name); err != nil {
		return err
	}
	empty, err := s.storage.IsBucketEmpty(name)
	if err != nil {
		return err
	}
	if !empty {
		return conflict("BucketNotEmpty", "Bucket '%s' is not empty", name)
	}
	if err := s.store.DeleteBucket(r.Context(), name); err != nil {
		return err
	}
	if err := s.storage.RemoveBucketDirectory(name); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// rotateBucketKey mints a brand-new key pair under the *current* master key without ever decrypting
// the old one - which is what makes rotation double as master-key-loss recovery. Objects are
// untouched; the old pair (and every presigned URL signed with it) stops working immediately.
func (s *Server) rotateBucketKey(w http.ResponseWriter, r *http.Request) error {
	b, err := s.requireBucket(r, r.PathValue("name"))
	if err != nil {
		return err
	}
	accessKey, secretKey := GenerateAccessKey(), GenerateSecretKey()
	if err := s.store.UpdateBucketKeys(r.Context(), b.Name, accessKey, s.crypto.EncryptSecret(secretKey)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, bucketCredentials{b.Name, accessKey, secretKey, b.CreatedAt, b.Description})
	return nil
}

// reindexBucket rebuilds the bucket's object index from disk, re-hashing every file.
func (s *Server) reindexBucket(w http.ResponseWriter, r *http.Request) error {
	b, err := s.requireBucket(r, r.PathValue("name"))
	if err != nil {
		return err
	}
	n, err := s.storage.ReindexBucket(r.Context(), b.Name, true)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"bucket": b.Name, "indexed": n})
	return nil
}

// ---- objects ----

// authedBucket returns the bucket bucketAuth resolved for this request. bucketAuth already rejects
// anything else, so a miss here means a routing bug - fail closed regardless.
func authedBucket(r *http.Request) (*authInfo, error) {
	a := authFrom(r)
	if a == nil || a.bucket.Name != r.PathValue("bucket") {
		return nil, &APIError{http.StatusUnauthorized, "Unauthorized",
			fmt.Sprintf("Request was not authenticated for bucket '%s'", r.PathValue("bucket"))}
	}
	return a, nil
}

func (s *Server) putObject(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	public := strings.EqualFold(r.Header.Get("X-S3Bender-Public"), "true")
	etag, err := s.storage.PutObject(r.Context(), a.bucket.Name, r.PathValue("key"), r.Body, r.Header.Get("Content-Type"), public)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
	return nil
}

// getObject serves GET and HEAD. http.ServeContent over the open file gives:
//   - zero-copy sendfile(2) from disk to socket for the body,
//   - Range requests (206), so <video>/<audio> can seek and large images start rendering
//     without waiting for the whole object,
//   - If-None-Match / If-Modified-Since → 304 with no body, so repeat loads cost a round trip only.
func (s *Server) getObject(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	f, obj, err := s.storage.OpenObject(r.Context(), a.bucket.Name, r.PathValue("key"))
	if err != nil {
		return err
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Type", obj.ContentType)
	h.Set("ETag", `"`+obj.ETag+`"`)
	h.Set("X-S3Bender-Public", strconv.FormatBool(obj.Public))
	h.Set("Cache-Control", cacheControl(a))
	http.ServeContent(w, r, "", obj.LastModified, f)
	return nil
}

// cacheControl lets browsers reuse what they're allowed to without ever serving bytes past the
// point the credential would have been checked again:
//   - presigned: cache privately for the URL's remaining lifetime (capped), then revalidate;
//   - public: shared caches may store it but must revalidate every time, so making it private
//     again takes effect immediately;
//   - header auth: private, revalidate every time (each request carries a fresh signature).
func cacheControl(a *authInfo) string {
	switch {
	case a.viaPublic:
		return "public, no-cache"
	case a.presignExpires > 0:
		remaining := time.Until(time.Unix(a.presignExpires, 0))
		return fmt.Sprintf("private, max-age=%d", int64(min(remaining, presignCacheCap).Seconds()))
	default:
		return "private, no-cache"
	}
}

func (s *Server) deleteObject(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	if err := s.storage.DeleteObject(r.Context(), a.bucket.Name, r.PathValue("key")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listObjects(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	limit := MaxListLimit
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil {
			return badRequest("InvalidRequest", "The value '%s' is not valid for limit.", v)
		}
	}
	resp, err := s.storage.ListObjects(r.Context(), a.bucket.Name, q.Get("prefix"), limit, q.Get("cursor"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) bucketStats(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	st, err := s.store.Stats(r.Context(), a.bucket.Name, r.URL.Query().Get("prefix"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}

// setACL flips visibility without re-uploading. It is the one endpoint that can grant public
// access, so it must never be reachable through the public-object bypass - bucketAuth only grants
// that to GET/HEAD on /objects/ paths.
func (s *Server) setACL(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	var req struct {
		Public bool `json:"public"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.storage.SetPublic(r.Context(), a.bucket.Name, r.PathValue("key"), req.Public); err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Server) presign(w http.ResponseWriter, r *http.Request) error {
	a, err := authedBucket(r)
	if err != nil {
		return err
	}
	var req struct {
		Key              string `json:"key"`
		Method           string `json:"method"`
		ExpiresInSeconds int64  `json:"expiresInSeconds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(req.Key) == "":
		return badRequest("InvalidRequest", "The Key field is required.")
	case req.Method == "":
		return badRequest("InvalidRequest", "The Method field is required.")
	case req.Method != "GET" && req.Method != "PUT":
		return badRequest("InvalidRequest", "must be GET or PUT")
	case req.ExpiresInSeconds < 1 || req.ExpiresInSeconds > 604800:
		return badRequest("InvalidRequest", "The field ExpiresInSeconds must be between 1 and 604800.")
	case req.ExpiresInSeconds > s.cfg.MaxPresignExpirySeconds:
		return badRequest("InvalidExpiry", "expiresInSeconds may not exceed %d", s.cfg.MaxPresignExpirySeconds)
	}

	secret, err := s.crypto.DecryptSecret(a.bucket.EncryptedSecretKey)
	if err != nil {
		return err
	}
	path := "/buckets/" + a.bucket.Name + "/objects/" + req.Key
	expires := time.Now().Unix() + req.ExpiresInSeconds
	signature := Sign(secret, StringToSign(req.Method, path, expires))

	baseURL := s.cfg.PublicBaseURL
	if strings.TrimSpace(baseURL) == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		baseURL = scheme + "://" + r.Host
	}
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = escapeDataString(seg)
	}
	url := strings.TrimRight(baseURL, "/") + strings.Join(segments, "/") +
		"?AccessKey=" + escapeDataString(a.bucket.AccessKey) +
		"&Expires=" + strconv.FormatInt(expires, 10) + "&Signature=" + signature

	writeJSON(w, http.StatusOK, map[string]any{
		"url": url, "method": req.Method, "expiresAt": time.Unix(expires, 0).UTC(),
	})
	return nil
}

// escapeDataString percent-encodes everything except RFC 3986 unreserved characters.
func escapeDataString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

const maxJSONBody = 1 << 20

func decodeJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxJSONBody {
		return &APIError{http.StatusRequestEntityTooLarge, "InvalidRequest", "Request body is too large"}
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return badRequest("InvalidRequest", "A non-empty request body is required.")
	}
	if err := json.Unmarshal(data, v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return badRequest("InvalidRequest", "The JSON value for '%s' is not valid.", typeErr.Field)
		}
		return badRequest("InvalidRequest", "Request body is not valid JSON.")
	}
	return nil
}
