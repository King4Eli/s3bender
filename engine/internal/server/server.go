package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
)

// Server wires config, the metadata store, object storage and crypto into an http.Handler. The
// same Server answers both the API port and the console port - see Handler.
type Server struct {
	cfg     Config
	store   *Store
	storage *Storage
	crypto  *Crypto
	console fs.FS
	log     *slog.Logger
}

// New validates config (failing closed on a missing/invalid master key), opens the database and
// prepares storage. console is the static UI served on the frontend port.
func New(cfg Config, console fs.FS, logger *slog.Logger) (*Server, error) {
	crypto, err := NewCrypto(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	storage, err := NewStorage(cfg.StorageRoot, store)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return &Server{cfg: cfg, store: store, storage: storage, crypto: crypto, console: console, log: logger}, nil
}

func (s *Server) Close() error { return s.store.Close() }

// Handler returns the full pipeline. Every API route is reachable on both ports - the console's own
// JS calls the whole API (admin panel included) same-origin from whichever port served it. Only the
// console's static assets are port-scoped: with serveConsole false (the plain API port) they 404.
func (s *Server) Handler(serveConsole bool) http.Handler {
	return s.recoverPanics(s.adminAuth(s.bucketAuth(s.router(serveConsole))))
}

// ReindexAll brings the object index up to date with what's on disk for every bucket. Non-forced:
// already-indexed buckets cost only a directory walk. Meant to run in the background at startup.
func (s *Server) ReindexAll(ctx context.Context) {
	buckets, err := s.store.ListBuckets(ctx)
	if err != nil {
		s.log.Error("background object reindex failed", "err", err)
		return
	}
	for _, b := range buckets {
		n, err := s.storage.ReindexBucket(ctx, b.Name, false)
		if err != nil {
			s.log.Error("background object reindex failed", "bucket", b.Name, "err", err)
			continue
		}
		if n > 0 {
			s.log.Info("reindexed objects", "bucket", b.Name, "count", n)
		}
	}
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) router(serveConsole bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/healthz":
			s.dispatch(w, r, map[string]handlerFunc{"GET": s.health})

		case p == "/admin/buckets":
			s.dispatch(w, r, map[string]handlerFunc{"GET": s.listBuckets, "POST": s.createBucket})

		case strings.HasPrefix(p, "/admin/buckets/"):
			name, action, _ := strings.Cut(strings.TrimPrefix(p, "/admin/buckets/"), "/")
			if name == "" {
				s.notFound(w)
				return
			}
			r.SetPathValue("name", name)
			switch action {
			case "":
				s.dispatch(w, r, map[string]handlerFunc{"DELETE": s.deleteBucket})
			case "rotate":
				s.dispatch(w, r, map[string]handlerFunc{"POST": s.rotateBucketKey})
			case "reindex":
				s.dispatch(w, r, map[string]handlerFunc{"POST": s.reindexBucket})
			default:
				s.notFound(w)
			}

		case strings.HasPrefix(p, "/buckets/"):
			bucket, rest, _ := strings.Cut(strings.TrimPrefix(p, "/buckets/"), "/")
			if bucket == "" {
				s.notFound(w)
				return
			}
			r.SetPathValue("bucket", bucket)
			switch {
			case rest == "objects":
				s.dispatch(w, r, map[string]handlerFunc{"GET": s.listObjects})
			case rest == "stats":
				s.dispatch(w, r, map[string]handlerFunc{"GET": s.bucketStats})
			case rest == "presign":
				s.dispatch(w, r, map[string]handlerFunc{"POST": s.presign})
			case strings.HasPrefix(rest, "objects/"):
				r.SetPathValue("key", strings.TrimPrefix(rest, "objects/"))
				s.dispatch(w, r, map[string]handlerFunc{
					"GET": s.getObject, "HEAD": s.getObject, "PUT": s.putObject, "DELETE": s.deleteObject,
				})
			case strings.HasPrefix(rest, "acl/"):
				r.SetPathValue("key", strings.TrimPrefix(rest, "acl/"))
				s.dispatch(w, r, map[string]handlerFunc{"PUT": s.setACL})
			default:
				s.notFound(w)
			}

		default:
			s.serveStatic(w, r, serveConsole)
		}
	})
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, routes map[string]handlerFunc) {
	h, ok := routes[r.Method]
	if !ok {
		allowed := make([]string, 0, len(routes))
		for m := range routes {
			allowed = append(allowed, m)
		}
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method+" is not allowed on this route")
		return
	}
	if err := h(w, r); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			writeError(w, apiErr.Status, apiErr.Code, apiErr.Message)
			return
		}
		s.internalError(w, r, err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("unhandled error", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "InternalError", "An unexpected error occurred")
}

func (s *Server) notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "NotFound", "No such route")
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.internalError(w, r, fmt.Errorf("panic: %v", v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// serveStatic serves the console UI. On the plain API port its assets deliberately 404 - external
// API clients have no use for them.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, serveConsole bool) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.console, name)
	if err != nil || !fs.ValidPath(name) {
		s.notFound(w)
		return
	}
	if !serveConsole {
		writeError(w, http.StatusNotFound, "NotFound", "No such route on this port")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method+" is not allowed on this route")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, startTime, bytes.NewReader(data))
}
