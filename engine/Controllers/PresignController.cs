using Microsoft.AspNetCore.Mvc;
using Microsoft.Extensions.Options;
using S3Bender.Api.Dtos;
using S3Bender.Api.Middleware;
using S3Bender.Api.Models;
using S3Bender.Api.Options;
using S3Bender.Api.Services;

namespace S3Bender.Api.Controllers;

[ApiController]
[Route("buckets/{bucket}")]
public class PresignController(PresignService presignService, IOptions<S3BenderOptions> options) : ControllerBase
{
    [HttpPost("presign")]
    public ActionResult<PresignResponse> Presign(string bucket, [FromBody] PresignRequest request)
    {
        var authenticated = RequireAuthenticated(bucket);
        var baseUrl = !string.IsNullOrWhiteSpace(options.Value.PublicBaseUrl)
            ? options.Value.PublicBaseUrl!
            : $"{Request.Scheme}://{Request.Host}";
        return presignService.Presign(authenticated, request, baseUrl);
    }

    private BucketEntity RequireAuthenticated(string bucket)
    {
        if (HttpContext.Items[BucketAuthMiddleware.BucketItem] is not BucketEntity authenticated || authenticated.Name != bucket)
            throw ApiException.Unauthorized("Unauthorized", $"Request was not authenticated for bucket '{bucket}'");
        return authenticated;
    }
}
