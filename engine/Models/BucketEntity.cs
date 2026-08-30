using System.ComponentModel.DataAnnotations;

namespace S3Bender.Api.Models;

public class BucketEntity
{
    [Key, MaxLength(63)]
    public string Name { get; set; } = default!;

    /// <summary>Optional free-text note set at creation time; purely for humans browsing the console.</summary>
    [MaxLength(200)]
    public string? Description { get; set; }

    [Required, MaxLength(64)]
    public string AccessKey { get; set; } = default!;

    /// <summary>Base64 AES-GCM ciphertext of the bucket's secret key, encrypted with the server master key.</summary>
    [Required, MaxLength(512)]
    public string EncryptedSecretKey { get; set; } = default!;

    public DateTimeOffset CreatedAt { get; set; }
}
