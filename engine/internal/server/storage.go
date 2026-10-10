package server

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxListLimit is the hard ceiling on objects returned by one list call, matching S3's max-keys.
const MaxListLimit = 1000

const defaultContentType = "application/octet-stream"

// Storage is the object layer: raw bytes at {root}/{bucket}/{key}, a JSON sidecar at
// {root}/.meta/{bucket}/{key} carrying what can't be derived from the bytes (Content-Type,
// visibility, the upload-time MD5), and a row per object in the index (see Store).
//
// Reads (list, stat, the public-object auth check) go through the index. Bytes and sidecars stay
// the source of truth: the index is written alongside every PUT/DELETE/ACL change and can be
// rebuilt from disk by ReindexBucket. The .meta tree lives outside {bucket}/ so it never shows up
// in a directory walk or in IsBucketEmpty.
type Storage struct {
	root     string
	metaRoot string
	store    *Store

	// One reindex per bucket at a time - the startup pass, a first-list self-heal and an admin
	// reindex can target the same bucket at once; the second caller waits, then finds little to do.
	reindexLocks sync.Map // bucket -> *sync.Mutex
}

func NewStorage(root string, store *Store) (*Storage, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Storage{root: abs, metaRoot: filepath.Join(abs, ".meta"), store: store}, nil
}

// objectMeta is the sidecar. Field names are PascalCase because that is what the previous engine
// wrote; encoding/json matches them case-insensitively on read, so camelCase sidecars load too.
type objectMeta struct {
	ContentType *string
	Public      bool
	ETag        *string `json:",omitempty"`
}

// ObjectSummary is both the list-entry JSON shape and what GET/HEAD need to answer.
type ObjectSummary struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
	ETag         string    `json:"eTag"`
	ContentType  string    `json:"contentType"`
	Public       bool      `json:"public"`
}

func summaryOf(o *ObjectRow) ObjectSummary {
	s := ObjectSummary{Key: o.Key, Size: o.Size, LastModified: o.LastModified, ContentType: defaultContentType, Public: o.Public}
	if o.ETag != nil {
		s.ETag = *o.ETag
	}
	if o.ContentType != nil {
		s.ContentType = *o.ContentType
	}
	return s
}

func (s *Storage) bucketDir(bucket string) string { return filepath.Join(s.root, bucket) }

func (s *Storage) metaPath(bucket, key string) string { return filepath.Join(s.metaRoot, bucket, key) }

// ResolveObjectPath maps a key to its file, rejecting anything that could escape the bucket
// directory. This check is the security boundary against path traversal - keep it strict.
func (s *Storage) ResolveObjectPath(bucket, key string) (string, error) {
	if strings.TrimSpace(key) == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.ContainsRune(key, 0) {
		return "", badRequest("InvalidKey", "Object key is invalid")
	}
	dir := s.bucketDir(bucket)
	resolved := filepath.Join(dir, key)
	if !strings.HasPrefix(resolved, dir+string(filepath.Separator)) {
		return "", badRequest("InvalidKey", "Object key escapes bucket directory")
	}
	return resolved, nil
}

func (s *Storage) CreateBucketDirectory(bucket string) error {
	return os.MkdirAll(s.bucketDir(bucket), 0o755)
}

func (s *Storage) RemoveBucketDirectory(bucket string) error {
	err := os.Remove(s.bucketDir(bucket))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Storage) IsBucketEmpty(bucket string) (bool, error) {
	f, err := os.Open(s.bucketDir(bucket))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// PutObject streams body to disk (via a temp file + rename, so readers never see a partial
// object), hashing as it goes, and returns the hex MD5 ETag. Every PUT replaces Content-Type and
// visibility along with the bytes - re-uploading without public=true resets the object to private.
func (s *Storage) PutObject(ctx context.Context, bucket, key string, body io.Reader, contentType string, public bool) (string, error) {
	target, err := s.ResolveObjectPath(bucket, key)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	hash := md5.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), body); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}

	etag := hex.EncodeToString(hash.Sum(nil))
	ct := sanitizeContentType(contentType)
	if err := s.writeMeta(bucket, key, ct, public, &etag); err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	return etag, s.store.UpsertObject(ctx, &ObjectRow{
		Bucket: bucket, Key: key, Size: info.Size(), LastModified: info.ModTime().UTC(),
		ETag: &etag, ContentType: ct, Public: public,
	})
}

// OpenObject opens an object for reading and returns its metadata. The caller closes the file.
func (s *Storage) OpenObject(ctx context.Context, bucket, key string) (*os.File, ObjectSummary, error) {
	path, err := s.ResolveObjectPath(bucket, key)
	if err != nil {
		return nil, ObjectSummary{}, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ObjectSummary{}, noSuchKey(bucket, key)
	}
	if err != nil {
		return nil, ObjectSummary{}, err
	}
	if info, err := f.Stat(); err != nil || info.IsDir() {
		f.Close()
		return nil, ObjectSummary{}, noSuchKey(bucket, key)
	}
	summary, err := s.statIndexed(ctx, bucket, key, path)
	if err != nil {
		f.Close()
		return nil, ObjectSummary{}, err
	}
	return f, summary, nil
}

// statIndexed answers from the index, filling in a missing ETag or a missing row (drift: the file
// exists but the index doesn't know it) so the next listing sees it too.
func (s *Storage) statIndexed(ctx context.Context, bucket, key, path string) (ObjectSummary, error) {
	row, err := s.store.GetObject(ctx, bucket, key)
	if err != nil {
		return ObjectSummary{}, err
	}
	if row != nil {
		if row.ETag == nil {
			etag, err := computeETag(path)
			if err != nil {
				return ObjectSummary{}, err
			}
			row.ETag = &etag
			if err := s.store.SetObjectETag(ctx, bucket, key, etag); err != nil {
				return ObjectSummary{}, err
			}
		}
		return summaryOf(row), nil
	}
	row, err = s.indexFromDisk(bucket, key, path)
	if err != nil {
		return ObjectSummary{}, err
	}
	if err := s.store.UpsertObject(ctx, row); err != nil {
		return ObjectSummary{}, err
	}
	return summaryOf(row), nil
}

// SetPublic flips an existing object's visibility without touching its bytes or Content-Type.
func (s *Storage) SetPublic(ctx context.Context, bucket, key string, public bool) error {
	path, err := s.ResolveObjectPath(bucket, key)
	if err != nil {
		return err
	}
	if !isFile(path) {
		return noSuchKey(bucket, key)
	}
	meta := s.readMeta(bucket, key)
	if err := s.writeMeta(bucket, key, meta.ContentType, public, meta.ETag); err != nil {
		return err
	}
	row, err := s.store.GetObject(ctx, bucket, key)
	if err != nil {
		return err
	}
	if row == nil {
		if row, err = s.indexFromDisk(bucket, key, path); err != nil {
			return err
		}
	}
	row.Public = public
	return s.store.UpsertObject(ctx, row)
}

// IsPublic decides whether a GET/HEAD may skip signature verification. Invalid keys are never public.
func (s *Storage) IsPublic(ctx context.Context, bucket, key string) bool {
	if _, err := s.ResolveObjectPath(bucket, key); err != nil {
		return false
	}
	public, found, err := s.store.ObjectPublic(ctx, bucket, key)
	if err != nil {
		return false
	}
	if found {
		return public
	}
	return s.readMeta(bucket, key).Public
}

// DeleteObject is idempotent: deleting a missing key is not an error.
func (s *Storage) DeleteObject(ctx context.Context, bucket, key string) error {
	path, err := s.ResolveObjectPath(bucket, key)
	if err != nil {
		return err
	}
	metaPath := s.metaPath(bucket, key)
	if err := removeIfExists(path); err != nil {
		return err
	}
	removeEmptyParents(filepath.Dir(path), s.bucketDir(bucket))
	if err := removeIfExists(metaPath); err != nil {
		return err
	}
	removeEmptyParents(filepath.Dir(metaPath), filepath.Join(s.metaRoot, bucket))
	return s.store.DeleteObject(ctx, bucket, key)
}

type ListObjectsResponse struct {
	Objects     []ObjectSummary `json:"objects"`
	IsTruncated bool            `json:"isTruncated"`
	NextCursor  *string         `json:"nextCursor"`
	KeyCount    int             `json:"keyCount"`
}

// ListObjects returns one page of keys in order, strictly after cursor, from the index. If a
// bucket has files on disk but no index rows at all (e.g. the DB was restored without them), the
// first page triggers a one-off reindex so a listing is never silently empty.
func (s *Storage) ListObjects(ctx context.Context, bucket, prefix string, limit int, cursor string) (ListObjectsResponse, error) {
	limit = min(max(limit, 1), MaxListLimit)

	if cursor == "" && prefix == "" {
		has, err := s.store.HasObjects(ctx, bucket)
		if err != nil {
			return ListObjectsResponse{}, err
		}
		if !has && hasObjectFiles(s.bucketDir(bucket)) {
			if _, err := s.ReindexBucket(ctx, bucket, false); err != nil {
				return ListObjectsResponse{}, err
			}
		}
	}

	rows, err := s.store.ListObjects(ctx, bucket, prefix, cursor, limit+1)
	if err != nil {
		return ListObjectsResponse{}, err
	}
	resp := ListObjectsResponse{Objects: []ObjectSummary{}}
	if len(rows) > limit {
		rows = rows[:limit]
		resp.IsTruncated = true
		next := rows[limit-1].Key
		resp.NextCursor = &next
	}
	for _, r := range rows {
		resp.Objects = append(resp.Objects, summaryOf(r))
	}
	resp.KeyCount = len(resp.Objects)
	return resp, nil
}

// ReindexBucket rebuilds one bucket's index from its directory and sidecars: adds rows for files
// the index is missing, refreshes changed ones, drops rows whose file is gone. With force false
// (the startup pass) a row that already has an ETag and a matching size is left alone, so a
// restart doesn't re-hash everything; with force true (the admin endpoint) every file is re-hashed.
// Returns the number of rows written or removed.
func (s *Storage) ReindexBucket(ctx context.Context, bucket string, force bool) (int, error) {
	lock, _ := s.reindexLocks.LoadOrStore(bucket, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	dir := s.bucketDir(bucket)
	if !isDir(dir) {
		return s.store.DeleteBucketObjects(ctx, bucket)
	}
	existing, err := s.store.IndexedObjects(ctx, bucket)
	if err != nil {
		return 0, err
	}

	var upserts []*ObjectRow
	seen := map[string]bool{}
	err = walkObjectFiles(dir, func(path string, info fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		seen[key] = true
		row := existing[key]
		if !force && row != nil && row.ETag != nil && row.Size == info.Size() {
			return nil
		}
		fresh, err := s.indexFromDisk(bucket, key, path)
		if err != nil {
			return err
		}
		upserts = append(upserts, fresh)
		return nil
	})
	if err != nil {
		return 0, err
	}

	var deletes []string
	for key := range existing {
		if !seen[key] {
			deletes = append(deletes, key)
		}
	}
	if err := s.store.ApplyIndexChanges(ctx, bucket, upserts, deletes); err != nil {
		return 0, err
	}
	return len(upserts) + len(deletes), nil
}

// indexFromDisk builds (unsaved) an index row from a file and its sidecar, hashing the file only
// if the sidecar has no cached ETag - and caching the hash it just paid for.
func (s *Storage) indexFromDisk(bucket, key, path string) (*ObjectRow, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	meta := s.readMeta(bucket, key)
	if meta.ETag == nil {
		etag, err := computeETag(path)
		if err != nil {
			return nil, err
		}
		meta.ETag = &etag
		if err := s.writeMeta(bucket, key, meta.ContentType, meta.Public, meta.ETag); err != nil {
			return nil, err
		}
	}
	return &ObjectRow{
		Bucket: bucket, Key: key, Size: info.Size(), LastModified: info.ModTime().UTC(),
		ETag: meta.ETag, ContentType: meta.ContentType, Public: meta.Public,
	}, nil
}

func (s *Storage) writeMeta(bucket, key string, contentType *string, public bool, etag *string) error {
	path := s.metaPath(bucket, key)
	contentType = sanitizeContentTypePtr(contentType)
	if contentType == nil && !public && etag == nil {
		return removeIfExists(path)
	}
	data, err := json.Marshal(objectMeta{ContentType: contentType, Public: public, ETag: etag})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// readMeta tolerates a legacy sidecar that is a bare Content-Type string (written before the
// Public flag existed): it reads back as that Content-Type with Public false, so old objects stay
// private rather than silently becoming public.
func (s *Storage) readMeta(bucket, key string) objectMeta {
	data, err := os.ReadFile(s.metaPath(bucket, key))
	if err != nil {
		return objectMeta{}
	}
	raw := strings.TrimSpace(string(data))
	if strings.HasPrefix(raw, "{") {
		var m objectMeta
		if json.Unmarshal([]byte(raw), &m) == nil {
			return m
		}
	}
	if raw == "" {
		return objectMeta{}
	}
	return objectMeta{ContentType: &raw}
}

// sanitizeContentType drops anything that could smuggle a control character into the
// Content-Type response header replayed on every future download.
func sanitizeContentType(ct string) *string {
	if strings.TrimSpace(ct) == "" || utf8.RuneCountInString(ct) > 255 || strings.IndexFunc(ct, unicode.IsControl) >= 0 {
		return nil
	}
	return &ct
}

func sanitizeContentTypePtr(ct *string) *string {
	if ct == nil {
		return nil
	}
	return sanitizeContentType(*ct)
}

func computeETag(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// walkObjectFiles visits every object file under dir, skipping in-flight upload temp files.
func walkObjectFiles(dir string, fn func(path string, info fs.FileInfo) error) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".upload-") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(path, info)
	})
}

var errStopWalk = errors.New("stop")

func hasObjectFiles(dir string) bool {
	if !isDir(dir) {
		return false
	}
	err := walkObjectFiles(dir, func(string, fs.FileInfo) error { return errStopWalk })
	return errors.Is(err, errStopWalk)
}

// removeEmptyParents cleans up the intermediate directories a key like "docs/hello.txt" created,
// so an empty "docs/" doesn't keep the bucket looking non-empty and block its deletion.
func removeEmptyParents(dir, boundary string) {
	for dir != boundary && strings.HasPrefix(dir, boundary+string(filepath.Separator)) {
		if os.Remove(dir) != nil { // fails if non-empty (or already gone) - either way, stop
			return
		}
		dir = filepath.Dir(dir)
	}
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
