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
