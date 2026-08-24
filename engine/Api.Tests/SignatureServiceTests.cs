using S3Bender.Api.Services;
using Xunit;

namespace S3Bender.Api.Tests;

public class SignatureServiceTests
{
    private readonly SignatureService _signatureService = new();

    [Fact]
    public void SameInputsProduceMatchingSignature()
    {
        var stringToSign = _signatureService.StringToSignForHeader("GET", "/buckets/demo/objects/a.txt", 1_700_000_000L);
        var signature = _signatureService.Sign("top-secret", stringToSign);
        var recomputed = _signatureService.Sign("top-secret", stringToSign);
        Assert.True(_signatureService.Matches(signature, recomputed));
    }

    [Fact]
    public void DifferentSecretProducesMismatch()
    {
        var stringToSign = _signatureService.StringToSignForPresign("GET", "/buckets/demo/objects/a.txt", 1_700_000_000L);
        var a = _signatureService.Sign("secret-a", stringToSign);
        var b = _signatureService.Sign("secret-b", stringToSign);
        Assert.False(_signatureService.Matches(a, b));
    }

    [Fact]
    public void DifferentPathProducesMismatch()
    {
        const string secret = "top-secret";
        var sigA = _signatureService.Sign(secret,
            _signatureService.StringToSignForHeader("GET", "/buckets/demo/objects/a.txt", 1_700_000_000L));
        var sigB = _signatureService.Sign(secret,
            _signatureService.StringToSignForHeader("GET", "/buckets/demo/objects/b.txt", 1_700_000_000L));
        Assert.False(_signatureService.Matches(sigA, sigB));
    }
}
