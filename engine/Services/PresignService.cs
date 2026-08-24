using Microsoft.Extensions.Options;
using S3Bender.Api.Dtos;
using S3Bender.Api.Models;
using S3Bender.Api.Options;

namespace S3Bender.Api.Services;

public class PresignService(SignatureService signatureService, BucketService bucketService, IOptions<S3BenderOptions> options)
{
    public PresignResponse Presign(BucketEntity bucket, PresignRequest request, string externalBaseUrl)
    {
        if (request.ExpiresInSeconds > options.Value.Signing.MaxPresignExpirySeconds)
            throw ApiException.BadRequest("InvalidExpiry",
                $"expiresInSeconds may not exceed {options.Value.Signing.MaxPresignExpirySeconds}");

        var path = $"/buckets/{bucket.Name}/objects/{request.Key}";
        var expiresAt = DateTimeOffset.UtcNow.ToUnixTimeSeconds() + request.ExpiresInSeconds;
        var secret = bucketService.DecryptedSecretFor(bucket);
        var stringToSign = signatureService.StringToSignForPresign(request.Method, path, expiresAt);
        var signature = signatureService.Sign(secret, stringToSign);

        var encodedPath = string.Join('/', path.Split('/').Select(Uri.EscapeDataString));
        var url = $"{externalBaseUrl.TrimEnd('/')}{encodedPath}?AccessKey={Uri.EscapeDataString(bucket.AccessKey)}" +
                  $"&Expires={expiresAt}&Signature={signature}";

        return new PresignResponse(url, request.Method, DateTimeOffset.FromUnixTimeSeconds(expiresAt));
    }
}
