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
        var response = await bucketService.CreateBucketAsync(request.Name);
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
}
