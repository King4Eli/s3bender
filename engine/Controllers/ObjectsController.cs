using Microsoft.AspNetCore.Mvc;
using S3Bender.Api.Dtos;
using S3Bender.Api.Middleware;
using S3Bender.Api.Models;
using S3Bender.Api.Services;

namespace S3Bender.Api.Controllers;

/// <summary>
/// Object read/write/list endpoints, scoped to a bucket. Authentication is handled upstream by
/// BucketAuthMiddleware, which attaches the resolved BucketEntity to HttpContext.Items.
/// </summary>
[ApiController]
[Route("buckets/{bucket}")]
public class ObjectsController(ObjectStorageService storageService) : ControllerBase
{
    [HttpPut("objects/{**key}")]
    [RequestSizeLimit(long.MaxValue)]
    public async Task<IActionResult> PutObject([FromRoute] string bucket, [FromRoute] string key)
    {
        RequireAuthenticated(bucket);
        // EnableBuffering() (Program.cs) lets us rewind - something earlier in the pipeline may
        // already have read past the start of the stream for a Content-Type it had no reason to.
        Request.Body.Position = 0;
        // Every PUT replaces visibility along with the bytes - re-uploading without this header
        // resets the object to private, matching S3's own ACL-doesn't-carry-forward behavior.
        var isPublic = string.Equals(Request.Headers["X-S3Bender-Public"].FirstOrDefault(), "true", StringComparison.OrdinalIgnoreCase);
        var etag = await storageService.PutObjectAsync(bucket, key, Request.Body, Request.ContentType, isPublic);
        Response.Headers.ETag = $"\"{etag}\"";
        return Ok();
    }

    [HttpGet("objects/{**key}")]
    public IActionResult GetObject(string bucket, string key)
    {
        RequireAuthenticated(bucket);
        var summary = storageService.StatObject(bucket, key);
        Response.Headers.ETag = $"\"{summary.ETag}\"";
        Response.Headers["X-S3Bender-Public"] = summary.Public ? "true" : "false";
        var stream = storageService.GetObject(bucket, key);
        return File(stream, summary.ContentType);
    }

    [HttpHead("objects/{**key}")]
    public IActionResult HeadObject(string bucket, string key)
    {
        RequireAuthenticated(bucket);
        var summary = storageService.StatObject(bucket, key);
        Response.Headers.ETag = $"\"{summary.ETag}\"";
        Response.Headers["X-S3Bender-Public"] = summary.Public ? "true" : "false";
        Response.ContentLength = summary.Size;
        Response.ContentType = summary.ContentType;
        return Ok();
    }

    [HttpDelete("objects/{**key}")]
    public IActionResult DeleteObject(string bucket, string key)
    {
        RequireAuthenticated(bucket);
        storageService.DeleteObject(bucket, key);
        return NoContent();
    }

    /// <summary>
    /// One page of the bucket's keys, ordered, served from the object index. <paramref name="limit"/>
    /// defaults to and is capped at <see cref="ObjectStorageService.MaxListLimit"/> (S3's max-keys).
    /// When the response's <c>isTruncated</c> is true, pass its <c>nextCursor</c> back as
    /// <paramref name="cursor"/> to fetch the following page.
    /// </summary>
    [HttpGet("objects")]
    public async Task<ActionResult<ListObjectsResponse>> ListObjects(
        string bucket, [FromQuery] string? prefix, [FromQuery] int limit = ObjectStorageService.MaxListLimit,
        [FromQuery] string? cursor = null)
    {
        RequireAuthenticated(bucket);
        return await storageService.ListObjectsAsync(bucket, prefix, limit, cursor);
    }

    /// <summary>Whole-bucket (or whole-prefix) totals for the console's summary bar.</summary>
    [HttpGet("stats")]
    public async Task<ActionResult<BucketStats>> Stats(string bucket, [FromQuery] string? prefix)
    {
        RequireAuthenticated(bucket);
        return await storageService.GetStatsAsync(bucket, prefix);
    }

    /// <summary>
    /// Flips an existing object's visibility without re-uploading it. Header auth only - unlike
    /// GET/HEAD, this never accepts a presigned or public-object bypass, since it's the one
    /// endpoint that can grant public access in the first place.
    /// </summary>
    [HttpPut("acl/{**key}")]
    public IActionResult SetAcl([FromRoute] string bucket, [FromRoute] string key, [FromBody] SetAclRequest request)
    {
        RequireAuthenticated(bucket);
        storageService.SetPublic(bucket, key, request.Public);
        return Ok();
    }

    private void RequireAuthenticated(string bucket)
    {
        if (HttpContext.Items[BucketAuthMiddleware.BucketItem] is not BucketEntity authenticated || authenticated.Name != bucket)
            throw ApiException.Unauthorized("Unauthorized", $"Request was not authenticated for bucket '{bucket}'");
    }
}
