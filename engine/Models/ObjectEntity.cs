using System.ComponentModel.DataAnnotations;

namespace S3Bender.Api.Models;

/// <summary>
/// One row per stored object - the queryable index over what's on disk. The object's bytes still
/// live only at {storage root}/{bucket}/{key} (this table is never the byte store), and the JSON
/// sidecar at {storage root}/.meta/{bucket}/{key} is still the portable on-disk metadata record;
/// this row is a cache rebuilt from those two by ObjectStorageService.ReindexBucketAsync, so a
/// listing costs an indexed `WHERE Bucket = ? AND Key > ? ORDER BY Key LIMIT ?` instead of walking
/// and hashing the entire bucket directory on every call.
/// </summary>
public class ObjectEntity
{
    [MaxLength(63)]
    public string Bucket { get; set; } = default!;

    [MaxLength(1024)]
    public string Key { get; set; } = default!;

    public long Size { get; set; }

    public DateTimeOffset LastModified { get; set; }

    /// <summary>
    /// Hex MD5 of the object's bytes. Null only transiently: a row discovered by a reindex (or
    /// inserted as a drift fallback on read) before its hash has been computed. StatObject fills it
    /// in on first access; a completed reindex leaves none behind.
    /// </summary>
    [MaxLength(32)]
    public string? ETag { get; set; }

    [MaxLength(255)]
    public string? ContentType { get; set; }

    public bool Public { get; set; }
}
