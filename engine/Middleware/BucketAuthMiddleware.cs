using System.Net;
using System.Text.RegularExpressions;
using Microsoft.Extensions.Options;
using S3Bender.Api.Dtos;
using S3Bender.Api.Models;
using S3Bender.Api.Options;
using S3Bender.Api.Services;

namespace S3Bender.Api.Middleware;

/// <summary>
/// Authenticates every /buckets/{name}/** request against that bucket's own access/secret key pair.
///
/// Two accepted credential forms (see /_docs/auth-and-signing.md):
///   1. Authorization header: S3BENDER-HMAC-SHA256 AccessKey=..,Timestamp=..,Signature=..
///   2. Presigned query string (GET/PUT/HEAD only): ?AccessKey=..&Expires=..&Signature=..
///
/// A third path exists only for GET/HEAD: an object marked public (see ObjectStorageService.IsPublic,
/// set via the `X-S3Bender-Public` upload header or `PUT /buckets/{bucket}/acl/{key}`) skips
/// signature verification entirely, the same as S3's public-read ACL - see
/// /_docs/public-and-private-objects.md. Every other verb, and every unauthenticated request for a
/// private object, still requires a valid credential.
/// </summary>
public partial class BucketAuthMiddleware(RequestDelegate next)
{
    public const string BucketItem = "S3Bender.Bucket";

    [GeneratedRegex(@"^/buckets/([^/]+)(/.*)?$")]
    private static partial Regex BucketPathRegex();

    [GeneratedRegex(@"^/buckets/([^/]+)/objects/(.+)$")]
    private static partial Regex ObjectPathRegex();

    [GeneratedRegex(@"^S3BENDER-HMAC-SHA256\s+AccessKey=([^,]+),\s*Timestamp=([^,]+),\s*Signature=([0-9a-fA-F]+)$")]
    private static partial Regex AuthHeaderRegex();

    public async Task InvokeAsync(HttpContext context, BucketService bucketService, SignatureService signatureService,
        ObjectStorageService storageService, IOptions<S3BenderOptions> options)
    {
        var decodedPath = Uri.UnescapeDataString(context.Request.Path.Value ?? "");
        var pathMatch = BucketPathRegex().Match(decodedPath);
        if (!pathMatch.Success)
        {
            await next(context);
            return;
        }
        var bucketName = pathMatch.Groups[1].Value;
        var method = context.Request.Method;

        if (method is "GET" or "HEAD")
        {
            var publicBucket = await TryFindPublicObjectBucket(bucketName, decodedPath, bucketService, storageService);
            if (publicBucket is not null)
            {
                context.Items[BucketItem] = publicBucket;
                await next(context);
                return;
            }
        }

        var queryAccessKey = context.Request.Query["AccessKey"].FirstOrDefault();
        var headerAccessKey = ExtractHeaderAccessKey(context);
        var bucket = await bucketService.FindByAccessKeyAsync(queryAccessKey ?? headerAccessKey);

        if (bucket is null || bucket.Name != bucketName)
        {
            await Reject(context, HttpStatusCode.Unauthorized, "InvalidAccessKey",
                $"No such bucket, or credentials do not belong to bucket '{bucketName}'");
            return;
        }

        var secret = bucketService.DecryptedSecretFor(bucket);

        var hasPresignParams = context.Request.Query.ContainsKey("AccessKey")
            && context.Request.Query.ContainsKey("Expires")
            && context.Request.Query.ContainsKey("Signature");

        bool authenticated;
        if (hasPresignParams)
        {
            authenticated = (method is "GET" or "PUT" or "HEAD") && VerifyPresigned(context, decodedPath, secret, signatureService);
        }
        else
        {
            authenticated = VerifyHeader(context, decodedPath, secret, signatureService, options.Value.Signing.ClockSkewSeconds);
        }

        if (!authenticated)
        {
            await Reject(context, HttpStatusCode.Forbidden, "SignatureMismatch",
                "Request signature is invalid, expired, or malformed");
            return;
        }

        context.Items[BucketItem] = bucket;
        await next(context);
    }

    /// <summary>
    /// Looks up the bucket for a GET/HEAD on an object that exists and is marked public - callers
    /// dispatch straight to `next(context)` with no signature check at all when this returns
    /// non-null. Returns null for every other case (wrong path shape, private object, unknown
    /// bucket), leaving the normal credential-checking flow in InvokeAsync to run instead.
    /// </summary>
    private static async Task<BucketEntity?> TryFindPublicObjectBucket(string bucketName, string decodedPath,
        BucketService bucketService, ObjectStorageService storageService)
    {
        var objectMatch = ObjectPathRegex().Match(decodedPath);
        if (!objectMatch.Success) return null;

        var key = objectMatch.Groups[2].Value;
        if (!storageService.IsPublic(bucketName, key)) return null;

        return await bucketService.FindByNameAsync(bucketName);
    }

    private static bool VerifyPresigned(HttpContext context, string path, string secret, SignatureService signatureService)
    {
        if (!long.TryParse(context.Request.Query["Expires"], out var expires)) return false;
        if (DateTimeOffset.UtcNow.ToUnixTimeSeconds() > expires) return false;

        var stringToSign = signatureService.StringToSignForPresign(context.Request.Method, path, expires);
        var expected = signatureService.Sign(secret, stringToSign);
        return signatureService.Matches(expected, context.Request.Query["Signature"]);
    }

    private static bool VerifyHeader(HttpContext context, string path, string secret, SignatureService signatureService, long clockSkewSeconds)
    {
        var header = context.Request.Headers.Authorization.FirstOrDefault();
        if (header is null) return false;

        var match = AuthHeaderRegex().Match(header.Trim());
        if (!match.Success) return false;

        if (!long.TryParse(match.Groups[2].Value, out var timestamp)) return false;
        var skew = Math.Abs(DateTimeOffset.UtcNow.ToUnixTimeSeconds() - timestamp);
        if (skew > clockSkewSeconds) return false;

        var stringToSign = signatureService.StringToSignForHeader(context.Request.Method, path, timestamp);
        var expected = signatureService.Sign(secret, stringToSign);
        return signatureService.Matches(expected, match.Groups[3].Value);
    }

    private static string? ExtractHeaderAccessKey(HttpContext context)
    {
        var header = context.Request.Headers.Authorization.FirstOrDefault();
        if (header is null) return null;
        var match = AuthHeaderRegex().Match(header.Trim());
        return match.Success ? match.Groups[1].Value : null;
    }

    private static async Task Reject(HttpContext context, HttpStatusCode status, string code, string message)
    {
        context.Response.StatusCode = (int)status;
        context.Response.ContentType = "application/json";
        await context.Response.WriteAsJsonAsync(ErrorResponse.Of(code, message));
    }
}
