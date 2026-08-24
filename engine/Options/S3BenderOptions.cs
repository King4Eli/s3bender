namespace S3Bender.Api.Options;

/// <summary>
/// Bound from the "S3Bender" config section (see appsettings.json / environment variables in
/// Program.cs). Mirrors engine/src/main/java/com/s3bender/config/S3BenderProperties.java.
/// </summary>
public class S3BenderOptions
{
    public const string SectionName = "S3Bender";

    public StorageOptions Storage { get; set; } = new();
    public AuthOptions Auth { get; set; } = new();
    public SigningOptions Signing { get; set; } = new();
    public string? PublicBaseUrl { get; set; }

    public class StorageOptions
    {
        public string Root { get; set; } = "./data/objects";
    }

    public class AuthOptions
    {
        public string? AdminApiKey { get; set; }
        public string? MasterKey { get; set; }
    }

    public class SigningOptions
    {
        public long ClockSkewSeconds { get; set; } = 900;
        public long MaxPresignExpirySeconds { get; set; } = 604800;
    }
}
