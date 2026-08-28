using System.Net;
using System.Net.Mime;
using System.Security.Cryptography;
using System.Text.Json;
using Microsoft.Extensions.Options;
using S3Bender.Api.Dtos;
using S3Bender.Api.Options;

namespace S3Bender.Api.Services;

/// <summary>
/// Stores object bytes on the local filesystem as {storage.root}/{bucket}/{key}, and each
/// object's Content-Type (as declared on upload) plus its public/private visibility as a small
/// JSON sidecar file under a separate {storage.root}/.meta/{bucket}/{key} tree, so both survive
/// to be replayed on download - without the Content-Type, browsers have nothing but MIME-sniffing
/// to go on and can't reliably render an &lt;img&gt;/&lt;video&gt;/&lt;audio&gt; element pointed at
/// a presigned URL; without the visibility flag, BucketAuthMiddleware has no way to know a GET/HEAD
/// should skip signature verification.
///
/// The .meta tree is intentionally outside {bucket}/ so it never shows up in ListObjects() or
/// IsBucketEmpty() - those only ever look inside the bucket's own directory.
///
/// Keys are validated to stay within their bucket directory (no traversal, no absolute paths).
/// </summary>
public class ObjectStorageService
{
    /// <summary>
    /// A bare (non-JSON) sidecar file predates the `Public` flag - ReadMeta treats its whole
    /// content as a legacy Content-Type string and defaults Public to false, so objects written
    /// before this existed stay private rather than silently becoming public.
    /// </summary>
    private sealed record ObjectMeta(string? ContentType, bool Public);

    private readonly string _root;
    private readonly string _metaRoot;

    public ObjectStorageService(IOptions<S3BenderOptions> options)
    {
        _root = Path.GetFullPath(options.Value.Storage.Root);
        _metaRoot = Path.Combine(_root, ".meta");
        Directory.CreateDirectory(_root);
        Directory.CreateDirectory(_metaRoot);
    }

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

        using (var md5 = MD5.Create())
        {
            await using (var cryptoStream = new CryptoStream(body, md5, CryptoStreamMode.Read))
            await using (var fileStream = File.Create(tmp))
            {
                await cryptoStream.CopyToAsync(fileStream);
            }
            File.Move(tmp, target, overwrite: true);
            WriteMeta(bucket, key, contentType, isPublic);
            return Convert.ToHexString(md5.Hash!).ToLowerInvariant();
        }
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

        var info = new FileInfo(path);
        var meta = ReadMeta(bucket, key);
        return new ObjectSummary(key, info.Length, info.LastWriteTimeUtc, ComputeEtag(path),
            meta.ContentType ?? MediaTypeNames.Application.Octet, meta.Public);
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
        WriteMeta(bucket, key, meta.ContentType, isPublic);
    }

    /// <summary>Used by BucketAuthMiddleware to decide whether a GET/HEAD needs a valid signature at all.</summary>
    public bool IsPublic(string bucket, string key) => ReadMeta(bucket, key).Public;

    public void DeleteObject(string bucket, string key)
    {
        var path = ResolveObjectPath(bucket, key);
        var bucketDir = BucketDir(bucket);
        var metaPath = MetaPath(bucket, key);

        if (File.Exists(path)) File.Delete(path);
        RemoveEmptyParents(Path.GetDirectoryName(path)!, bucketDir);

        if (File.Exists(metaPath)) File.Delete(metaPath);
        RemoveEmptyParents(Path.GetDirectoryName(metaPath)!, Path.Combine(_metaRoot, bucket));
    }

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

    public List<ObjectSummary> ListObjects(string bucket, string? prefix)
    {
        var dir = BucketDir(bucket);
        if (!Directory.Exists(dir)) return [];

        return Directory.EnumerateFiles(dir, "*", SearchOption.AllDirectories)
            .Select(p => Path.GetRelativePath(dir, p).Replace('\\', '/'))
            .Where(k => string.IsNullOrEmpty(prefix) || k.StartsWith(prefix, StringComparison.Ordinal))
            .OrderBy(k => k, StringComparer.Ordinal)
            .Select(k =>
            {
                var p = Path.Combine(dir, k);
                var info = new FileInfo(p);
                var meta = ReadMeta(bucket, k);
                return new ObjectSummary(k, info.Length, info.LastWriteTimeUtc, ComputeEtag(p),
                    meta.ContentType ?? MediaTypeNames.Application.Octet, meta.Public);
            })
            .ToList();
    }

    private void WriteMeta(string bucket, string key, string? contentType, bool isPublic)
    {
        var metaPath = MetaPath(bucket, key);
        // Control characters would otherwise be replayed verbatim into a response header on
        // every future download; drop rather than store anything that could smuggle one.
        var validContentType = !string.IsNullOrWhiteSpace(contentType) && contentType.Length <= 255
            && !contentType.Any(char.IsControl)
            ? contentType
            : null;

        if (validContentType is null && !isPublic)
        {
            if (File.Exists(metaPath)) File.Delete(metaPath);
            return;
        }

        Directory.CreateDirectory(Path.GetDirectoryName(metaPath)!);
        File.WriteAllText(metaPath, JsonSerializer.Serialize(new ObjectMeta(validContentType, isPublic)));
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
