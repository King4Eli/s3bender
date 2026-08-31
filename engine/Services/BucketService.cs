using Microsoft.EntityFrameworkCore;
using S3Bender.Api.Data;
using S3Bender.Api.Dtos;
using S3Bender.Api.Models;

namespace S3Bender.Api.Services;

public class BucketService(S3BenderDbContext db, CryptoService crypto, ObjectStorageService storage)
{
    public async Task<CreateBucketResponse> CreateBucketAsync(string name, string? description = null)
    {
        if (await db.Buckets.AnyAsync(b => b.Name == name))
            throw ApiException.Conflict("BucketAlreadyExists", $"Bucket '{name}' already exists");

        var accessKey = crypto.GenerateAccessKey();
        var secretKey = crypto.GenerateSecretKey();
        var now = DateTimeOffset.UtcNow;
        var trimmedDescription = string.IsNullOrWhiteSpace(description) ? null : description.Trim();

        db.Buckets.Add(new BucketEntity
        {
            Name = name,
            AccessKey = accessKey,
            EncryptedSecretKey = crypto.EncryptSecret(secretKey),
            CreatedAt = now,
            Description = trimmedDescription,
        });

        try
        {
            await db.SaveChangesAsync();
            storage.CreateBucketDirectory(name);
        }
        catch
        {
            db.Buckets.Remove(db.Buckets.Local.First(b => b.Name == name));
            throw;
        }

        return new CreateBucketResponse(name, accessKey, secretKey, now, trimmedDescription);
    }

    /// <summary>
    /// Replaces a bucket's access/secret key pair in place - objects are untouched, only the
    /// credentials change. The old pair stops authenticating immediately (including any
    /// outstanding presigned URLs signed with it), so this is also the only way to recover a
    /// bucket after its secret key is lost - see /_docs/how-to-use.md.
    /// </summary>
    public async Task<CreateBucketResponse> RotateBucketKeyAsync(string name)
    {
        var entity = await db.Buckets.FindAsync(name)
            ?? throw ApiException.NotFound("NoSuchBucket", $"Bucket '{name}' does not exist");

        var accessKey = crypto.GenerateAccessKey();
        var secretKey = crypto.GenerateSecretKey();

        entity.AccessKey = accessKey;
        entity.EncryptedSecretKey = crypto.EncryptSecret(secretKey);
        await db.SaveChangesAsync();

        return new CreateBucketResponse(entity.Name, accessKey, secretKey, entity.CreatedAt, entity.Description);
    }

    public async Task DeleteBucketAsync(string name)
    {
        var entity = await db.Buckets.FindAsync(name)
            ?? throw ApiException.NotFound("NoSuchBucket", $"Bucket '{name}' does not exist");

        if (!storage.IsBucketEmpty(name))
            throw ApiException.Conflict("BucketNotEmpty", $"Bucket '{name}' is not empty");

        // The bucket is empty on disk, so there should be no index rows either - clear them anyway
        // so a stale row can never outlive its bucket (and block a same-name recreate's listing).
        await db.Objects.Where(o => o.Bucket == name).ExecuteDeleteAsync();
        db.Buckets.Remove(entity);
        await db.SaveChangesAsync();
        storage.RemoveBucketDirectory(name);
    }

    /// <summary>
    /// Rebuilds the object index for an existing bucket from its on-disk bytes and sidecars
    /// (<c>force: true</c> - re-stat and re-hash every file). Used by
    /// <c>POST /admin/buckets/{name}/reindex</c> to migrate a bucket that predates the index or to
    /// repair drift after files were added or removed out of band.
    /// </summary>
    public async Task<ReindexResponse> ReindexBucketAsync(string name)
    {
        _ = await db.Buckets.FindAsync(name)
            ?? throw ApiException.NotFound("NoSuchBucket", $"Bucket '{name}' does not exist");
        var indexed = await storage.ReindexBucketAsync(name, force: true);
        return new ReindexResponse(name, indexed);
    }

    public async Task<List<BucketSummary>> ListBucketsAsync() =>
        await db.Buckets.OrderBy(b => b.Name).Select(b => new BucketSummary(b.Name, b.CreatedAt, b.Description)).ToListAsync();

    public async Task<BucketEntity> RequireBucketAsync(string name) =>
        await db.Buckets.FindAsync(name) ?? throw ApiException.NotFound("NoSuchBucket", $"Bucket '{name}' does not exist");

    public Task<BucketEntity?> FindByAccessKeyAsync(string? accessKey) =>
        accessKey is null ? Task.FromResult<BucketEntity?>(null) : db.Buckets.FirstOrDefaultAsync(b => b.AccessKey == accessKey);

    public async Task<BucketEntity?> FindByNameAsync(string name) => await db.Buckets.FindAsync(name);

    public string DecryptedSecretFor(BucketEntity bucket) => crypto.DecryptSecret(bucket.EncryptedSecretKey);
}
