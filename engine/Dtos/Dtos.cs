using System.ComponentModel.DataAnnotations;

namespace S3Bender.Api.Dtos;

public record CreateBucketRequest(
    [Required, RegularExpression("^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$",
        ErrorMessage = "must be 3-63 chars, lowercase alphanumeric and hyphens, not starting/ending with a hyphen")]
    string Name,
    [MaxLength(200, ErrorMessage = "must be 200 characters or fewer")]
    string? Description = null);

public record CreateBucketResponse(string Name, string AccessKey, string SecretKey, DateTimeOffset CreatedAt, string? Description = null);

public record BucketSummary(string Name, DateTimeOffset CreatedAt, string? Description = null);

public record ObjectSummary(string Key, long Size, DateTimeOffset LastModified, string ETag, string ContentType, bool Public);

/// <summary>
/// One page of a bucket listing. <see cref="NextCursor"/> is the last key on this page - pass it
/// back as `?cursor=` to fetch the next page; it is null exactly when <see cref="IsTruncated"/> is
/// false. <see cref="KeyCount"/> is the number of objects on this page, not in the whole bucket.
/// </summary>
public record ListObjectsResponse(IReadOnlyList<ObjectSummary> Objects, bool IsTruncated, string? NextCursor, int KeyCount);

/// <summary>Whole-bucket (or whole-prefix) totals, served from the object index in one query.</summary>
public record BucketStats(long Objects, long TotalBytes, int TopLevelFolders, int TopLevelFiles);

public record ReindexResponse(string Bucket, int Indexed);

public record SetAclRequest(bool Public);

public record PresignRequest(
    [Required] string Key,
    [RegularExpression("GET|PUT", ErrorMessage = "must be GET or PUT")] string Method,
    [Range(1, 604800)] long ExpiresInSeconds);

public record PresignResponse(string Url, string Method, DateTimeOffset ExpiresAt);

public record ErrorResponse(string Code, string Message, DateTimeOffset Timestamp)
{
    public static ErrorResponse Of(string code, string message) => new(code, message, DateTimeOffset.UtcNow);
}
