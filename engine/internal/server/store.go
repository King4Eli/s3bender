package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Store is the SQLite metadata database: one row per bucket, plus the object index (see
// _docs/data-model.md). The schema - table/column names and the TEXT timestamp format - matches
// what the previous C#/EF Core engine created, so an existing s3bender.db opens unchanged.
type Store struct {
	db *sql.DB
}

type Bucket struct {
	Name               string
	Description        *string
	AccessKey          string
	EncryptedSecretKey string
	CreatedAt          time.Time
}

// ObjectRow is one entry in the object index - a cache over the bytes and sidecars on disk, never
// the source of truth. ETag is nil only transiently (discovered by a reindex, not yet hashed).
type ObjectRow struct {
	Bucket       string
	Key          string
	Size         int64
	LastModified time.Time
	ETag         *string
	ContentType  *string
	Public       bool
}

// EF Core's SQLite DateTimeOffset format ("yyyy-MM-dd HH:mm:ss.FFFFFFFzzz"). Parsing also accepts
// no fractional part, which is what EF writes for whole seconds.
const dbTimeLayout = "2006-01-02 15:04:05.9999999-07:00"

func formatDBTime(t time.Time) string { return t.UTC().Format(dbTimeLayout) }

func parseDBTime(s string) time.Time {
	for _, layout := range []string{dbTimeLayout, time.RFC3339Nano, "2006-01-02 15:04:05.9999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS "Buckets" (
		"Name" TEXT NOT NULL CONSTRAINT "PK_Buckets" PRIMARY KEY,
		"Description" TEXT NULL,
		"AccessKey" TEXT NOT NULL,
		"EncryptedSecretKey" TEXT NOT NULL,
		"CreatedAt" TEXT NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "IX_Buckets_AccessKey" ON "Buckets" ("AccessKey")`,
	// Composite PK (Bucket, Key) is also the covering index for every listing query:
	// WHERE Bucket = ? AND Key > ? ORDER BY Key LIMIT ?.
	`CREATE TABLE IF NOT EXISTS "Objects" (
		"Bucket" TEXT NOT NULL,
		"Key" TEXT NOT NULL,
		"Size" INTEGER NOT NULL,
		"LastModified" TEXT NOT NULL,
		"ETag" TEXT NULL,
		"ContentType" TEXT NULL,
		"Public" INTEGER NOT NULL,
		CONSTRAINT "PK_Objects" PRIMARY KEY ("Bucket", "Key")
	)`,
}

func OpenStore(path string) (*Store, error) {
	full, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	// busy_timeout: SQLite is single-writer, so a write that collides with another (e.g. the
	// background reindex) waits its turn instead of failing with SQLITE_BUSY. _txlock=immediate
	// takes the write lock at BEGIN, so a read-then-write transaction can't deadlock on upgrade.
	dsn := "file:" + (&url.URL{Path: full}).EscapedPath() +
		"?_pragma=busy_timeout(30000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	for _, ddl := range schema {
		if _, err := db.Exec(ddl); err != nil {
			db.Close()
			return nil, fmt.Errorf("init schema: %w", err)
		}
	}
	// Databases created before Description existed get it patched in (ADD COLUMN is metadata-only).
	if _, err := db.Exec(`ALTER TABLE "Buckets" ADD COLUMN "Description" TEXT NULL`); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrate Buckets.Description: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

func isConstraintViolation(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == 19 // SQLITE_CONSTRAINT and its extended codes
}

// ---- buckets ----

const bucketCols = `"Name", "Description", "AccessKey", "EncryptedSecretKey", "CreatedAt"`

func scanBucket(row interface{ Scan(...any) error }) (*Bucket, error) {
	var b Bucket
	var created string
	if err := row.Scan(&b.Name, &b.Description, &b.AccessKey, &b.EncryptedSecretKey, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	b.CreatedAt = parseDBTime(created)
	return &b, nil
}

func (s *Store) InsertBucket(ctx context.Context, b *Bucket) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO "Buckets" (`+bucketCols+`) VALUES (?, ?, ?, ?, ?)`,
		b.Name, b.Description, b.AccessKey, b.EncryptedSecretKey, formatDBTime(b.CreatedAt))
	if isConstraintViolation(err) {
		return conflict("BucketAlreadyExists", "Bucket '%s' already exists", b.Name)
	}
	return err
}

func (s *Store) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	return scanBucket(s.db.QueryRowContext(ctx, `SELECT `+bucketCols+` FROM "Buckets" WHERE "Name" = ?`, name))
}

func (s *Store) BucketByAccessKey(ctx context.Context, accessKey string) (*Bucket, error) {
	return scanBucket(s.db.QueryRowContext(ctx, `SELECT `+bucketCols+` FROM "Buckets" WHERE "AccessKey" = ?`, accessKey))
}

func (s *Store) ListBuckets(ctx context.Context) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+bucketCols+` FROM "Buckets" ORDER BY "Name"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Bucket
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) UpdateBucketKeys(ctx context.Context, name, accessKey, encryptedSecret string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE "Buckets" SET "AccessKey" = ?, "EncryptedSecretKey" = ? WHERE "Name" = ?`,
		accessKey, encryptedSecret, name)
	return err
}

// DeleteBucket removes the bucket row and any index rows it still has, so a stale row can never
// outlive its bucket (and leak into a same-name recreate's listing).
func (s *Store) DeleteBucket(ctx context.Context, name string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM "Objects" WHERE "Bucket" = ?`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM "Buckets" WHERE "Name" = ?`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- object index ----

const objectCols = `"Bucket", "Key", "Size", "LastModified", "ETag", "ContentType", "Public"`

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func scanObject(row interface{ Scan(...any) error }) (*ObjectRow, error) {
	var o ObjectRow
	var modified string
	if err := row.Scan(&o.Bucket, &o.Key, &o.Size, &modified, &o.ETag, &o.ContentType, &o.Public); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	o.LastModified = parseDBTime(modified)
	return &o, nil
}

func (s *Store) GetObject(ctx context.Context, bucket, key string) (*ObjectRow, error) {
	return scanObject(s.db.QueryRowContext(ctx,
		`SELECT `+objectCols+` FROM "Objects" WHERE "Bucket" = ? AND "Key" = ?`, bucket, key))
}

func upsertObject(ctx context.Context, x execer, o *ObjectRow) error {
	_, err := x.ExecContext(ctx, `INSERT INTO "Objects" (`+objectCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT ("Bucket", "Key") DO UPDATE SET
			"Size" = excluded."Size", "LastModified" = excluded."LastModified", "ETag" = excluded."ETag",
			"ContentType" = excluded."ContentType", "Public" = excluded."Public"`,
		o.Bucket, o.Key, o.Size, formatDBTime(o.LastModified), o.ETag, o.ContentType, o.Public)
	return err
}

func (s *Store) UpsertObject(ctx context.Context, o *ObjectRow) error {
	return upsertObject(ctx, s.db, o)
}

func (s *Store) SetObjectETag(ctx context.Context, bucket, key, etag string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE "Objects" SET "ETag" = ? WHERE "Bucket" = ? AND "Key" = ?`, etag, bucket, key)
	return err
}

// ObjectPublic reports the indexed visibility of one object; found is false if it has no row.
func (s *Store) ObjectPublic(ctx context.Context, bucket, key string) (public, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT "Public" FROM "Objects" WHERE "Bucket" = ? AND "Key" = ?`, bucket, key).Scan(&public)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return public, err == nil, err
}

func (s *Store) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM "Objects" WHERE "Bucket" = ? AND "Key" = ?`, bucket, key)
	return err
}

func (s *Store) HasObjects(ctx context.Context, bucket string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM "Objects" WHERE "Bucket" = ? LIMIT 1`, bucket).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// prefixFilter matches keys starting with prefix, case-sensitively (LIKE would fold ASCII case).
const prefixFilter = ` AND (? = '' OR substr("Key", 1, length(?)) = ?)`

// ListObjects returns up to limit rows with Key > cursor (and starting with prefix), in key order.
func (s *Store) ListObjects(ctx context.Context, bucket, prefix, cursor string, limit int) ([]*ObjectRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+objectCols+` FROM "Objects"
		WHERE "Bucket" = ? AND "Key" > ?`+prefixFilter+` ORDER BY "Key" LIMIT ?`,
		bucket, cursor, prefix, prefix, prefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ObjectRow
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type BucketStats struct {
	Objects         int64 `json:"objects"`
	TotalBytes      int64 `json:"totalBytes"`
	TopLevelFolders int   `json:"topLevelFolders"`
	TopLevelFiles   int   `json:"topLevelFiles"`
}

// Stats aggregates the whole bucket (or prefix) in one query. The folder/file split is S3-style
// with delimiter "/" over the full key: no slash = top-level file, text before the first slash =
// a top-level folder, counted once.
func (s *Store) Stats(ctx context.Context, bucket, prefix string) (BucketStats, error) {
	var st BucketStats
	err := s.db.QueryRowContext(ctx, `SELECT
			COUNT(*),
			COALESCE(SUM("Size"), 0),
			COUNT(DISTINCT CASE WHEN instr("Key", '/') > 0 THEN substr("Key", 1, instr("Key", '/') - 1) END),
			COALESCE(SUM(CASE WHEN instr("Key", '/') = 0 THEN 1 ELSE 0 END), 0)
		FROM "Objects" WHERE "Bucket" = ?`+prefixFilter,
		bucket, prefix, prefix, prefix).Scan(&st.Objects, &st.TotalBytes, &st.TopLevelFolders, &st.TopLevelFiles)
	return st, err
}

// IndexedObjects returns every index row for a bucket, keyed by object key.
func (s *Store) IndexedObjects(ctx context.Context, bucket string) (map[string]*ObjectRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+objectCols+` FROM "Objects" WHERE "Bucket" = ?`, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ObjectRow{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out[o.Key] = o
	}
	return out, rows.Err()
}

// ApplyIndexChanges writes a reindex pass's upserts and deletions in one transaction.
func (s *Store) ApplyIndexChanges(ctx context.Context, bucket string, upserts []*ObjectRow, deletes []string) error {
	if len(upserts) == 0 && len(deletes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, o := range upserts {
		if err := upsertObject(ctx, tx, o); err != nil {
			return err
		}
	}
	for _, key := range deletes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM "Objects" WHERE "Bucket" = ? AND "Key" = ?`, bucket, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteBucketObjects(ctx context.Context, bucket string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM "Objects" WHERE "Bucket" = ?`, bucket)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
