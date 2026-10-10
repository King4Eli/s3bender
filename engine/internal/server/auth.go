package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const adminKeyHeader = "X-Admin-Api-Key"

var (
	bucketPathRe = regexp.MustCompile(`^/buckets/([^/]+)(/.*)?$`)
	objectPathRe = regexp.MustCompile(`^/buckets/([^/]+)/objects/(.+)$`)
	authHeaderRe = regexp.MustCompile(`^S3BENDER-HMAC-SHA256\s+AccessKey=([^,]+),\s*Timestamp=([^,]+),\s*Signature=([0-9a-fA-F]+)$`)
)

// authInfo is attached to the request context by bucketAuth once a /buckets/{name}/** request is
// allowed through.
type authInfo struct {
	bucket *Bucket
	// viaPublic: no credential at all - a GET/HEAD on an object marked public.
	viaPublic bool
	// presignExpires: the Expires of the presigned URL used, or 0 for header auth.
	presignExpires int64
}

type authKey struct{}

func authFrom(r *http.Request) *authInfo {
	a, _ := r.Context().Value(authKey{}).(*authInfo)
	return a
}

// adminAuth guards every /admin/** route with the shared X-Admin-Api-Key secret.
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin" && !strings.HasPrefix(r.URL.Path, "/admin/") {
			next.ServeHTTP(w, r)
			return
		}
		if strings.TrimSpace(s.cfg.AdminAPIKey) == "" {
			writeError(w, http.StatusInternalServerError, "AdminKeyNotConfigured", "Server admin API key is not configured")
			return
		}
		provided, ok := r.Header[http.CanonicalHeaderKey(adminKeyHeader)]
		if !ok || len(provided) == 0 || subtle.ConstantTimeCompare([]byte(s.cfg.AdminAPIKey), []byte(provided[0])) != 1 {
			writeError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid "+adminKeyHeader+" header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bucketAuth authenticates every /buckets/{name}/** request against that bucket's own key pair.
// Accepted forms (see _docs/auth-and-signing.md):
//  1. Authorization: S3BENDER-HMAC-SHA256 AccessKey=..,Timestamp=..,Signature=..
//  2. Presigned query (GET/PUT/HEAD only): ?AccessKey=..&Expires=..&Signature=..
//  3. Nothing at all, for GET/HEAD on an object marked public - see
//     _docs/public-and-private-objects.md.
//
// The signed PATH is r.URL.Path: the request path percent-decoded exactly once.
func (s *Server) bucketAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		m := bucketPathRe.FindStringSubmatch(path)
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}
		bucketName := m[1]
		ctx := r.Context()

		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if om := objectPathRe.FindStringSubmatch(path); om != nil {
				// Look the bucket up first so a public check never touches the filesystem for a
				// bucket name that isn't real.
				if b, err := s.store.GetBucket(ctx, bucketName); err == nil && b != nil && s.storage.IsPublic(ctx, b.Name, om[2]) {
					next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, authKey{}, &authInfo{bucket: b, viaPublic: true})))
					return
				}
			}
		}

		query := r.URL.Query()
		header := r.Header.Get("Authorization")
		headerMatch := authHeaderRe.FindStringSubmatch(strings.TrimSpace(header))

		accessKey := ""
		switch {
		case query.Has("AccessKey"):
			accessKey = query.Get("AccessKey")
		case headerMatch != nil:
			accessKey = headerMatch[1]
		}
		var bucket *Bucket
		if accessKey != "" {
			b, err := s.store.BucketByAccessKey(ctx, accessKey)
			if err != nil {
				s.internalError(w, r, err)
				return
			}
			bucket = b
		}
		if bucket == nil || bucket.Name != bucketName {
			writeError(w, http.StatusUnauthorized, "InvalidAccessKey",
				"No such bucket, or credentials do not belong to bucket '"+bucketName+"'")
			return
		}
		secret, err := s.crypto.DecryptSecret(bucket.EncryptedSecretKey)
		if err != nil {
			s.internalError(w, r, err)
			return
		}

		info := &authInfo{bucket: bucket}
		var ok bool
		if query.Has("AccessKey") && query.Has("Expires") && query.Has("Signature") {
			switch r.Method {
			case http.MethodGet, http.MethodPut, http.MethodHead:
				info.presignExpires, ok = verifyPresigned(r.Method, path, secret, query.Get("Expires"), query.Get("Signature"))
			}
		} else {
			ok = verifyHeader(r.Method, path, secret, headerMatch, s.cfg.ClockSkewSeconds)
		}
		if !ok {
			writeError(w, http.StatusForbidden, "SignatureMismatch", "Request signature is invalid, expired, or malformed")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, authKey{}, info)))
	})
}

func verifyPresigned(method, path, secret, expiresParam, signature string) (int64, bool) {
	expires, err := strconv.ParseInt(expiresParam, 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return 0, false
	}
	return expires, SignatureMatches(Sign(secret, StringToSign(method, path, expires)), signature)
}

func verifyHeader(method, path, secret string, match []string, clockSkewSeconds int64) bool {
	if match == nil {
		return false
	}
	ts, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil {
		return false
	}
	skew := time.Now().Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > clockSkewSeconds {
		return false
	}
	return SignatureMatches(Sign(secret, StringToSign(method, path, ts)), match[3])
}
