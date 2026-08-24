using Microsoft.EntityFrameworkCore;
using S3Bender.Api.Models;

namespace S3Bender.Api.Data;

public class S3BenderDbContext(DbContextOptions<S3BenderDbContext> options) : DbContext(options)
{
    public DbSet<BucketEntity> Buckets => Set<BucketEntity>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.Entity<BucketEntity>()
            .HasIndex(b => b.AccessKey)
            .IsUnique();
    }
}
