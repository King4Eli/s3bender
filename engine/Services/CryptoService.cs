using System.Security.Cryptography;
using System.Text;
using Microsoft.Extensions.Options;
using S3Bender.Api.Options;

namespace S3Bender.Api.Services;

/// <summary>
/// Encrypts per-bucket secret keys at rest (AES-256-GCM) using a master key supplied out of band
/// (S3BENDER_MASTER_KEY), and generates the random access/secret key pairs issued on bucket creation.
/// </summary>
public class CryptoService
{
    private const int GcmTagBytes = 16;
    private const int GcmNonceBytes = 12;

    private readonly byte[] _masterKey;

    public CryptoService(IOptions<S3BenderOptions> options)
    {
        var encoded = options.Value.Auth.MasterKey;
        if (string.IsNullOrWhiteSpace(encoded))
            throw new InvalidOperationException("S3BENDER_MASTER_KEY is not set. Generate one with: openssl rand -base64 32");

        _masterKey = Convert.FromBase64String(encoded);
        if (_masterKey.Length != 32)
            throw new InvalidOperationException("S3BENDER_MASTER_KEY must decode to exactly 32 bytes (AES-256)");
    }

    public string EncryptSecret(string plaintext)
    {
        var nonce = RandomNumberGenerator.GetBytes(GcmNonceBytes);
        var plainBytes = Encoding.UTF8.GetBytes(plaintext);
        var cipherBytes = new byte[plainBytes.Length];
        var tag = new byte[GcmTagBytes];

        using var aesGcm = new AesGcm(_masterKey, GcmTagBytes);
        aesGcm.Encrypt(nonce, plainBytes, cipherBytes, tag);

        var combined = new byte[nonce.Length + cipherBytes.Length + tag.Length];
        Buffer.BlockCopy(nonce, 0, combined, 0, nonce.Length);
        Buffer.BlockCopy(cipherBytes, 0, combined, nonce.Length, cipherBytes.Length);
        Buffer.BlockCopy(tag, 0, combined, nonce.Length + cipherBytes.Length, tag.Length);
        return Convert.ToBase64String(combined);
    }

    public string DecryptSecret(string encoded)
    {
        var combined = Convert.FromBase64String(encoded);
        var nonce = combined[..GcmNonceBytes];
        var tag = combined[^GcmTagBytes..];
        var cipherBytes = combined[GcmNonceBytes..^GcmTagBytes];
        var plainBytes = new byte[cipherBytes.Length];

        using var aesGcm = new AesGcm(_masterKey, GcmTagBytes);
        aesGcm.Decrypt(nonce, cipherBytes, tag, plainBytes);
        return Encoding.UTF8.GetString(plainBytes);
    }

    public string GenerateAccessKey() => "AK" + RandomBase64Url(16);

    public string GenerateSecretKey() => RandomBase64Url(32);

    private static string RandomBase64Url(int byteLength) =>
        Convert.ToBase64String(RandomNumberGenerator.GetBytes(byteLength))
            .TrimEnd('=').Replace('+', '-').Replace('/', '_');
}
