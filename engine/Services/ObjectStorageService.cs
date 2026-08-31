using System.Collections.Concurrent;
using System.Net.Mime;
using System.Security.Cryptography;
using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using S3Bender.Api.Data;
using S3Bender.Api.Dtos;
using S3Bender.Api.Models;
using S3Bender.Api.Options;

namespace S3Bender.Api.Services;

/// <summary>
/// The object layer: raw bytes on the local filesystem at {storage.root}/{bucket}/{key}, an
/// {storage.root}/.meta/{bucket}/{key} JSON sidecar carrying the three things that can't be derived
/// from the bytes (Content-Type, public/private visibility, and the upload-time MD5), and a row per
/// object in the <c>Objects</c> table that indexes all of it.
///
/// Reads (list, stat, the public-object auth check) go through the index - a listing is an
/// <c>ORDER BY Key LIMIT n</c> over the composite PK, never a directory walk. The index is a cache:
/// it is written alongside every PUT/DELETE/ACL change, and <see cref="ReindexBucketAsync"/> can
/// rebuild it from scratch by walking the bucket directory and its sidecars (used by the startup
/// pass and <c>POST /admin/buckets/{name}/reindex</c>). Bytes and sidecars remain the source of
/// truth, so dropping the SQLite file and reindexing loses nothing.
///
/// The .meta tree is intentionally outside {bucket}/ so it never shows up in a directory walk or in
/// IsBucketEmpty(). Keys are validated to stay within their bucket directory (no traversal, no
/// absolute paths).
/// </summary>
public class ObjectStorageService(IOptions<S3BenderOptions> options, S3BenderDbContext db)
{
    /// <summary>Hard ceiling on objects returned by one list call, matching S3's max-keys.</summary>
    public const int MaxListLimit = 1000;

    /// <summary>
    /// One reindex per bucket at a time, process-wide - the startup pass, a first-list self-heal,
    /// and an admin reindex can all target the same bucket at once, and letting them run in
    /// parallel just means duplicated hashing and SQLite write contention. Second caller waits, then
    /// finds the work already done.
    /// </summary>
    private static readonly ConcurrentDictionary<string, SemaphoreSlim> ReindexLocks = new();

    /// <summary>
    /// A bare (non-JSON) sidecar file predates the `Public` flag - ReadMeta treats its whole
    /// content as a legacy Content-Type string and defaults Public to false, so objects written
    /// before this existed stay private rather than silently becoming public.
    /// </summary>
    private sealed record ObjectMeta(string? ContentType, bool Public, string? ETag = null);

    private readonly string _root = Path.GetFullPath(options.Value.Storage.Root);
    private readonly string _metaRoot = Path.Combine(Path.GetFullPath(options.Value.Storage.Root), ".meta");

    public void CreateBucketDirectory(string bucket) => Directory.CreateDirectory(BucketDir(bucket));

    public void RemoveBucketDirectory(string bucket)
    {
        var dir = BucketDir(bucket);
        if (Directory.Exists(dir)) Directory.Delete(dir);
    }

    public bool IsBucketEmpty(string bucket)
    {
        var dir = BucketDir(bucket);
        return !Directory.Exists(dir) || !Directory.EnumerateFileSystemEntries(dir).Any();
    }

    /// <summary>
    /// Streams the request body to disk and returns the resulting object's ETag (hex MD5).
    /// Overwrites any existing object (and its stored Content-Type/visibility) at the same key -
    /// re-uploading without passing `isPublic: true` again resets the object to private, matching
    /// S3's own behavior of not carrying an ACL forward across a fresh PUT.
    /// </summary>
    public async Task<string> PutObjectAsync(string bucket, string key, Stream body, string? contentType, bool isPublic = false)
    {
        var target = ResolveObjectPath(bucket, key);
        Directory.CreateDirectory(Path.GetDirectoryName(target)!);
        var tmp = Path.Combine(Path.GetDirectoryName(target)!, $".upload-{Guid.NewGuid():N}.tmp");

        using var md5 = MD5.Create();
        await using (var cryptoStream = new CryptoStream(body, md5, CryptoStreamMode.Read))
        await using (var fileStream = File.Create(tmp))
        {
            await cryptoStream.CopyToAsync(fileStream);
        }
        File.Move(tmp, target, overwrite: true);

        // The content hash is computed once here, on the bytes as they stream past, and cached in
        // both the sidecar and the index - so no read path ever has to re-hash the whole object.
        var etag = Convert.ToHexString(md5.Hash!).ToLowerInvariant();
        var validContentType = SanitizeContentType(contentType);
        WriteMeta(bucket, key, validContentType, isPublic, etag);

        var info = new FileInfo(target);
        await UpsertRowAsync(bucket, key, info.Length, info.LastWriteTimeUtc, etag, validContentType, isPublic);
        return etag;
    }

    public Stream GetObject(string bucket, string key)
    {
        var path = ResolveObjectPath(bucket, key);
        if (!File.Exists(path))
            throw ApiException.NotFound("NoSuchKey", $"Object '{key}' does not exist in bucket '{bucket}'");
        return File.OpenRead(path);
    }

    public ObjectSummary StatObject(string bucket, string key)
    {
        var path = ResolveObjectPath(bucket, key);
        if (!File.Exists(path))
            throw ApiException.NotFound("NoSuchKey", $"Object '{key}' does not exist in bucket '{bucket}'");

        var row = db.Objects.Find(bucket, key);
        if (row is not null)
        {
            if (row.ETag is null)
            {
                row.ETag = ComputeEtag(path);
                db.SaveChanges();
            }
            return ToSummary(row);
        }

        // Drift: the file (and maybe a sidecar) exists but the index doesn't know about it - index
        // it now so the next list sees it, and answer from what we just learned.
        var indexed = IndexFromDisk(bucket, key, path);
        try
        {
            db.SaveChanges();
        }
        catch (DbUpdateException)
        {
            // A concurrent read indexed the same key first - use its row.
            db.ChangeTracker.Clear();
            var winner = db.Objects.Find(bucket, key);
            if (winner is null) throw;
            indexed = winner;
        }
        return ToSummary(indexed);
    }

    /// <summary>
    /// Flips an existing object's visibility without re-uploading its bytes or Content-Type -
    /// backs the `PUT /buckets/{bucket}/acl/{key}` endpoint.
    /// </summary>
    public void SetPublic(string bucket, string key, bool isPublic)
    {
        var path = ResolveObjectPath(bucket, key);
        if (!File.Exists(path))
            throw ApiException.NotFound("NoSuchKey", $"Object '{key}' does not exist in bucket '{bucket}'");

        var meta = ReadMeta(bucket, key);
        WriteMeta(bucket, key, meta.ContentType, isPublic, meta.ETag);

        var row = db.Objects.Find(bucket, key) ?? IndexFromDisk(bucket, key, path);
        row.Public = isPublic;
        db.SaveChanges();
    }

    /// <summary>Used by BucketAuthMiddleware to decide whether a GET/HEAD needs a valid signature at all.</summary>
    public bool IsPublic(string bucket, string key)
    {
        var indexed = db.Objects.AsNoTracking()
            .Where(o => o.Bucket == bucket && o.Key == key)
            .Select(o => (bool?)o.Public)
            .FirstOrDefault();
        return indexed ?? ReadMeta(bucket, key).Public;
    }

    public void DeleteObject(string bucket, string key)
    {
        var path = ResolveObjectPath(bucket, key);
        var bucketDir = BucketDir(bucket);
        var metaPath = MetaPath(bucket, key);

        if (File.Exists(path)) File.Delete(path);
        RemoveEmptyParents(Path.GetDirectoryName(path)!, bucketDir);

        if (File.Exists(metaPath)) File.Delete(metaPath);
        RemoveEmptyParents(Path.GetDirectoryName(metaPath)!, Path.Combine(_metaRoot, bucket));

        var row = db.Objects.Find(bucket, key);
        if (row is not null)
        {
            db.Objects.Remove(row);
            db.SaveChanges();
        }
    }

    /// <summary>
    /// One page of keys, ordered, starting strictly after <paramref name="cursor"/> (a plain last-key
    /// marker). Served entirely from the index. If the index has no rows for a bucket whose directory
    /// is non-empty - e.g. the SQLite file was restored without its object rows - the first page
    /// triggers a one-off reindex and retries, so a listing is never silently empty.
    /// </summary>
    public async Task<ListObjectsResponse> ListObjectsAsync(string bucket, string? prefix, int limit, string? cursor)
    {
        limit = Math.Clamp(limit, 1, MaxListLimit);

        if (string.IsNullOrEmpty(cursor) && string.IsNullOrEmpty(prefix)
            && !await db.Objects.AnyAsync(o => o.Bucket == bucket)
            && Directory.Exists(BucketDir(bucket))
            && EnumerateObjectFiles(BucketDir(bucket)).Any())
        {
            await ReindexBucketAsync(bucket, force: false);
        }

        var rows = await PageQuery(bucket, prefix, cursor)
            .OrderBy(o => o.Key)
            .Take(limit + 1)
            .ToListAsync();

        var truncated = rows.Count > limit;
        var page = truncated ? rows.Take(limit).ToList() : rows;
        var next = truncated ? page[^1].Key : null;
        return new ListObjectsResponse(page.Select(ToSummary).ToList(), truncated, next, page.Count);
    }

    /// <summary>
    /// Whole-bucket (or whole-prefix) totals for the console's summary bar, from the index. Object
    /// count and byte total are SQL aggregates; the top-level folder/file split needs the keys
    /// themselves - fine for thousands, but at a much larger scale push it into a
    /// substr(Key, 1, instr(Key, '/')) aggregate instead of materializing every key.
    /// </summary>
    public async Task<BucketStats> GetStatsAsync(string bucket, string? prefix)
    {
        var q = PageQuery(bucket, prefix, cursor: null);
        var objects = await q.LongCountAsync();
        var totalBytes = await q.SumAsync(o => (long?)o.Size) ?? 0;

        var folders = new HashSet<string>(StringComparer.Ordinal);
        var files = 0;
        foreach (var key in await q.Select(o => o.Key).ToListAsync())
        {
            var slash = key.IndexOf('/');
            if (slash < 0) files++;
            else folders.Add(key[..slash]);
        }
        return new BucketStats(objects, totalBytes, folders.Count, files);
    }

    /// <summary>
    /// Rebuilds the index for one bucket from its directory + sidecars. Adds rows for files the
    /// index is missing, refreshes changed ones, drops rows whose file is gone. With
    /// <paramref name="force"/> false (the startup pass) a row that already has an ETag and a
    /// matching size is left untouched, so a restart doesn't re-hash the whole bucket; with it true
    /// (the admin endpoint) every file is re-stat'd and re-hashed. Returns the number of rows
    /// written or removed - which, on a forced pass, is every object file. Serialized per bucket so
    /// a startup pass, a self-heal, and an admin call can't pile onto the same bucket at once.
    /// </summary>
    public async Task<int> ReindexBucketAsync(string bucket, bool force)
    {
        var gate = ReindexLocks.GetOrAdd(bucket, _ => new SemaphoreSlim(1, 1));
        await gate.WaitAsync();
        try
        {
            return await ReindexBucketCoreAsync(bucket, force);
        }
        finally
        {
            gate.Release();
        }
    }

    private async Task<int> ReindexBucketCoreAsync(string bucket, bool force)
    {
        var dir = BucketDir(bucket);
        if (!Directory.Exists(dir))
            return await db.Objects.Where(o => o.Bucket == bucket).ExecuteDeleteAsync();

        var existing = await db.Objects.Where(o => o.Bucket == bucket).ToDictionaryAsync(o => o.Key);
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var changed = 0;

        foreach (var path in EnumerateObjectFiles(dir))
        {
            var key = Path.GetRelativePath(dir, path).Replace('\\', '/');
            seen.Add(key);
            var info = new FileInfo(path);
            existing.TryGetValue(key, out var row);

            if (!force && row is { ETag: not null } && row.Size == info.Length)
                continue;

            var meta = ReadMeta(bucket, key);
            var etag = meta.ETag ?? ComputeEtag(path);
            if (meta.ETag is null)
                WriteMeta(bucket, key, meta.ContentType, meta.Public, etag); // cache the hash we just paid for

            if (row is null)
                db.Objects.Add(new ObjectEntity
                {
                    Bucket = bucket, Key = key, Size = info.Length, LastModified = info.LastWriteTimeUtc,
                    ETag = etag, ContentType = meta.ContentType, Public = meta.Public,
                });
            else
            {
                row.Size = info.Length;
                row.LastModified = info.LastWriteTimeUtc;
                row.ETag = etag;
                row.ContentType = meta.ContentType;
                row.Public = meta.Public;
            }
            changed++;
        }

        foreach (var (key, row) in existing)
        {
            if (seen.Contains(key)) continue;
            db.Objects.Remove(row);
            changed++;
        }

        await db.SaveChangesAsync();
        return changed;
    }

    private IQueryable<ObjectEntity> PageQuery(string bucket, string? prefix, string? cursor)
    {
        var q = db.Objects.AsNoTracking().Where(o => o.Bucket == bucket);
        if (!string.IsNullOrEmpty(prefix)) q = q.Where(o => o.Key.StartsWith(prefix));
        if (!string.IsNullOrEmpty(cursor)) q = q.Where(o => string.Compare(o.Key, cursor) > 0);
        return q;
    }

    private async Task UpsertRowAsync(string bucket, string key, long size, DateTimeOffset lastModified,
        string etag, string? contentType, bool isPublic)
    {
        for (var attempt = 0; ; attempt++)
        {
            var row = await db.Objects.FindAsync(bucket, key);
            if (row is null)
                db.Objects.Add(new ObjectEntity
                {
                    Bucket = bucket, Key = key, Size = size, LastModified = lastModified,
                    ETag = etag, ContentType = contentType, Public = isPublic,
                });
            else
            {
                row.Size = size;
                row.LastModified = lastModified;
                row.ETag = etag;
                row.ContentType = contentType;
                row.Public = isPublic;
            }

            try
            {
                await db.SaveChangesAsync();
                return;
            }
            catch (DbUpdateException) when (attempt == 0)
            {
                // A concurrent request inserted this same key between the Find and the Save. Drop
                // the failed insert and retry once - the second pass takes the update branch.
                db.ChangeTracker.Clear();
            }
        }
    }

    /// <summary>
    /// Builds (and attaches, unsaved) an index row for a key whose bytes are on disk but which the
    /// index missed - reading its sidecar for Content-Type/visibility and hashing the file only if
    /// the sidecar has no cached ETag. Caller is responsible for SaveChanges.
    /// </summary>
    private ObjectEntity IndexFromDisk(string bucket, string key, string path)
    {
        var info = new FileInfo(path);
        var meta = ReadMeta(bucket, key);
        var etag = meta.ETag ?? ComputeEtag(path);
        if (meta.ETag is null)
            WriteMeta(bucket, key, meta.ContentType, meta.Public, etag);

        var row = new ObjectEntity
        {
            Bucket = bucket, Key = key, Size = info.Length, LastModified = info.LastWriteTimeUtc,
            ETag = etag, ContentType = meta.ContentType, Public = meta.Public,
        };
        db.Objects.Add(row);
        return row;
    }

    private static ObjectSummary ToSummary(ObjectEntity o) =>
        new(o.Key, o.Size, o.LastModified, o.ETag ?? "", o.ContentType ?? MediaTypeNames.Application.Octet, o.Public);

    private static IEnumerable<string> EnumerateObjectFiles(string dir) =>
        Directory.EnumerateFiles(dir, "*", SearchOption.AllDirectories)
            .Where(p => !Path.GetFileName(p).StartsWith(".upload-", StringComparison.Ordinal));

    /// <summary>
    /// Object keys like "docs/hello.txt" create intermediate directories; deleting the last file
    /// under one must clean those back up, otherwise an empty "docs/" directory keeps the bucket
    /// (or its .meta counterpart) looking non-empty and blocks deletion even after everything
    /// inside is gone.
    /// </summary>
    private static void RemoveEmptyParents(string? dir, string boundary)
    {
        while (dir is not null && !string.Equals(dir, boundary, StringComparison.Ordinal) && dir.StartsWith(boundary, StringComparison.Ordinal))
        {
            if (!Directory.Exists(dir) || Directory.EnumerateFileSystemEntries(dir).Any()) return;
            Directory.Delete(dir);
            dir = Path.GetDirectoryName(dir);
        }
    }

    /// <summary>
    /// Control characters would otherwise be replayed verbatim into a response header on every
    /// future download; drop rather than store anything that could smuggle one.
    /// </summary>
    private static string? SanitizeContentType(string? contentType) =>
        !string.IsNullOrWhiteSpace(contentType) && contentType.Length <= 255 && !contentType.Any(char.IsControl)
            ? contentType
            : null;

    private void WriteMeta(string bucket, string key, string? contentType, bool isPublic, string? etag)
    {
        var metaPath = MetaPath(bucket, key);
        var validContentType = SanitizeContentType(contentType);

        if (validContentType is null && !isPublic && etag is null)
        {
            if (File.Exists(metaPath)) File.Delete(metaPath);
            return;
        }

        Directory.CreateDirectory(Path.GetDirectoryName(metaPath)!);
        File.WriteAllText(metaPath, JsonSerializer.Serialize(new ObjectMeta(validContentType, isPublic, etag)));
    }

    private ObjectMeta ReadMeta(string bucket, string key)
    {
        var metaPath = MetaPath(bucket, key);
        if (!File.Exists(metaPath)) return new ObjectMeta(null, false);

        var raw = File.ReadAllText(metaPath).Trim();
        if (raw.StartsWith('{'))
        {
            try
            {
                var meta = JsonSerializer.Deserialize<ObjectMeta>(raw);
                if (meta is not null) return meta;
            }
            catch (JsonException) { /* fall through to legacy plain-Content-Type format below */ }
        }

        return new ObjectMeta(string.IsNullOrWhiteSpace(raw) ? null : raw, false);
    }

    private static string ComputeEtag(string path)
    {
        using var md5 = MD5.Create();
        using var stream = File.OpenRead(path);
        return Convert.ToHexString(md5.ComputeHash(stream)).ToLowerInvariant();
    }

    private string BucketDir(string bucket) => Path.GetFullPath(Path.Combine(_root, bucket));

    private string MetaPath(string bucket, string key) => Path.GetFullPath(Path.Combine(_metaRoot, bucket, key));

    private string ResolveObjectPath(string bucket, string key)
    {
        if (string.IsNullOrWhiteSpace(key) || key.StartsWith('/') || key.Contains("..") || key.Contains('\0'))
            throw ApiException.BadRequest("InvalidKey", "Object key is invalid");

        var bucketDir = BucketDir(bucket);
        var resolved = Path.GetFullPath(Path.Combine(bucketDir, key));
        if (!resolved.StartsWith(bucketDir, StringComparison.Ordinal))
            throw ApiException.BadRequest("InvalidKey", "Object key escapes bucket directory");
        return resolved;
    }
}
