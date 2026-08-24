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
        var etag = await storageService.PutObjectAsync(bucket, key, Request.Body, Request.ContentType);
        Response.Headers.ETag = $"\"{etag}\"";
        return Ok();
    }

    [HttpGet("objects/{**key}")]
    public IActionResult GetObject(string bucket, string key)
    {
        RequireAuthenticated(bucket);
        var summary = storageService.StatObject(bucket, key);
        Response.Headers.ETag = $"\"{summary.ETag}\"";
        var stream = storageService.GetObject(bucket, key);
        return File(stream, summary.ContentType);
    }

    [HttpHead("objects/{**key}")]
    public IActionResult HeadObject(string bucket, string key)
    {
        RequireAuthenticated(bucket);
        var summary = storageService.StatObject(bucket, key);
        Response.Headers.ETag = $"\"{summary.ETag}\"";
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

    [HttpGet("objects")]
    public ActionResult<List<ObjectSummary>> ListObjects(string bucket, [FromQuery] string? prefix)
    {
        RequireAuthenticated(bucket);
        return storageService.ListObjects(bucket, prefix);
    }

    private void RequireAuthenticated(string bucket)
    {
        if (HttpContext.Items[BucketAuthMiddleware.BucketItem] is not BucketEntity authenticated || authenticated.Name != bucket)
            throw ApiException.Unauthorized("Unauthorized", $"Request was not authenticated for bucket '{bucket}'");
    }
}
