using Microsoft.AspNetCore.Mvc;
using S3Bender.Api.Dtos;
using S3Bender.Api.Services;

namespace S3Bender.Api.Controllers;

/// <summary>
/// Control-plane API for provisioning buckets. Every route here requires the shared
/// X-Admin-Api-Key header (enforced by AdminAuthMiddleware) - it is the only credential that
/// spans buckets. Per-bucket access/secret keys returned by CreateBucket are shown exactly once.
/// </summary>
[ApiController]
[Route("admin/buckets")]
public class AdminController(BucketService bucketService) : ControllerBase
{
    [HttpPost]
    public async Task<ActionResult<CreateBucketResponse>> CreateBucket([FromBody] CreateBucketRequest request)
    {
        var response = await bucketService.CreateBucketAsync(request.Name, request.Description);
        return StatusCode(201, response);
    }

    [HttpGet]
    public async Task<ActionResult<List<BucketSummary>>> ListBuckets() => await bucketService.ListBucketsAsync();

    [HttpDelete("{name}")]
    public async Task<IActionResult> DeleteBucket(string name)
    {
        await bucketService.DeleteBucketAsync(name);
        return NoContent();
    }

    /// <summary>Mints a new access/secret key pair for an existing bucket; objects are untouched.</summary>
    [HttpPost("{name}/rotate")]
    public async Task<ActionResult<CreateBucketResponse>> RotateKey(string name) =>
        await bucketService.RotateBucketKeyAsync(name);

    /// <summary>
    /// Rebuilds the bucket's object index from what's on disk - re-stats and re-hashes every file.
    /// Run this once for a bucket that predates the index, or after adding/removing object files out
    /// of band. Cost scales with the bucket's total size (every byte is hashed); safe to re-run.
    /// </summary>
    [HttpPost("{name}/reindex")]
    public async Task<ActionResult<ReindexResponse>> Reindex(string name) =>
        await bucketService.ReindexBucketAsync(name);
}
