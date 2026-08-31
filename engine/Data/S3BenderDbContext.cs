using Microsoft.EntityFrameworkCore;
using S3Bender.Api.Models;

namespace S3Bender.Api.Data;

public class S3BenderDbContext(DbContextOptions<S3BenderDbContext> options) : DbContext(options)
{
    public DbSet<BucketEntity> Buckets => Set<BucketEntity>();
    public DbSet<ObjectEntity> Objects => Set<ObjectEntity>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.Entity<BucketEntity>()
            .HasIndex(b => b.AccessKey)
            .IsUnique();

        // Composite PK (Bucket, Key) - also the covering index for every listing query:
        // WHERE Bucket = @b AND Key > @cursor ORDER BY Key LIMIT @n.
        modelBuilder.Entity<ObjectEntity>()
            .HasKey(o => new { o.Bucket, o.Key });
    }
}
