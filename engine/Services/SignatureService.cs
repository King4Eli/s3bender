using System.Security.Cryptography;
using System.Text;

namespace S3Bender.Api.Services;

/// <summary>
/// HMAC-SHA256 request signing, shared by header-based auth and presigned URLs.
/// See /_docs/auth-and-signing.md for the exact string-to-sign layout.
/// </summary>
public class SignatureService
{
    public string Sign(string secretKey, string stringToSign)
    {
        using var hmac = new HMACSHA256(Encoding.UTF8.GetBytes(secretKey));
        var hash = hmac.ComputeHash(Encoding.UTF8.GetBytes(stringToSign));
        return Convert.ToHexString(hash).ToLowerInvariant();
    }

    /// <summary>Constant-time comparison to avoid leaking signature material via timing.</summary>
    public bool Matches(string? expectedHex, string? providedHex)
    {
        if (expectedHex is null || providedHex is null) return false;
        var a = Encoding.UTF8.GetBytes(expectedHex);
        var b = Encoding.UTF8.GetBytes(providedHex);
        return CryptographicOperations.FixedTimeEquals(a, b);
    }

    public string StringToSignForHeader(string method, string path, long timestampEpochSeconds) =>
        $"{method}\n{path}\n{timestampEpochSeconds}";

    public string StringToSignForPresign(string method, string path, long expiresEpochSeconds) =>
        $"{method}\n{path}\n{expiresEpochSeconds}";
}
