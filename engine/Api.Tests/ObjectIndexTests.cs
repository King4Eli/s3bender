using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using S3Bender.Api;
using S3Bender.Api.Data;
using S3Bender.Api.Dtos;
using Xunit;

namespace S3Bender.Api.Tests;

/// <summary>
/// Covers the SQLite object index that backs listing: server-side cursor pagination, prefix
/// filtering, the /stats aggregates, index upkeep on delete, and rebuilding the index from disk
/// (the admin reindex endpoint and the first-list self-heal).
/// </summary>
public class ObjectIndexTests : IClassFixture<WebApplicationFactory<Program>>, IDisposable
{
    private const string AdminKey = "test-admin-key";
    private readonly string _tempDir = Directory.CreateTempSubdirectory("s3bender-index-test-").FullName;
    private readonly WebApplicationFactory<Program> _factory;
    private readonly HttpClient _client;

    public ObjectIndexTests(WebApplicationFactory<Program> factory)
    {
        Environment.SetEnvironmentVariable("S3BENDER_ADMIN_API_KEY", AdminKey);
        Environment.SetEnvironmentVariable("S3BENDER_MASTER_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=");
        Environment.SetEnvironmentVariable("S3BENDER_STORAGE_ROOT", Path.Combine(_tempDir, "objects"));
        Environment.SetEnvironmentVariable("S3BENDER_DB_PATH", Path.Combine(_tempDir, "s3bender.db"));

        _factory = factory;
        _client = factory.CreateClient();
    }

    public void Dispose()
    {
        _client.Dispose();
        try { Directory.Delete(_tempDir, recursive: true); } catch { /* best-effort */ }
    }

    [Fact]
    public async Task ListPaginatesByCursorInKeyOrder()
    {
        var bucket = await CreateBucket("page-test");
        foreach (var k in new[] { "k4", "k0", "k2", "k1", "k3" })
            await Put(bucket, $"items/{k}", "x");

        var page1 = await List(bucket, "?prefix=items/&limit=2");
        Assert.Equal(new[] { "items/k0", "items/k1" }, page1.Objects.Select(o => o.Key));
        Assert.True(page1.IsTruncated);
        Assert.Equal("items/k1", page1.NextCursor);

        var page2 = await List(bucket, $"?prefix=items/&limit=2&cursor={Uri.EscapeDataString(page1.NextCursor!)}");
        Assert.Equal(new[] { "items/k2", "items/k3" }, page2.Objects.Select(o => o.Key));
        Assert.True(page2.IsTruncated);

        var page3 = await List(bucket, $"?prefix=items/&limit=2&cursor={Uri.EscapeDataString(page2.NextCursor!)}");
        Assert.Equal(new[] { "items/k4" }, page3.Objects.Select(o => o.Key));
        Assert.False(page3.IsTruncated);
        Assert.Null(page3.NextCursor);
    }

    [Fact]
    public async Task ListFiltersByPrefixAndClampsLimit()
    {
        var bucket = await CreateBucket("prefix-test");
        await Put(bucket, "a/one", "x");
        await Put(bucket, "a/two", "x");
        await Put(bucket, "b/one", "x");

        var underA = await List(bucket, "?prefix=a/");
        Assert.Equal(new[] { "a/one", "a/two" }, underA.Objects.Select(o => o.Key));

        var clampedLow = await List(bucket, "?limit=0");
        Assert.Single(clampedLow.Objects);
        Assert.True(clampedLow.IsTruncated);

        var clampedHigh = await List(bucket, "?limit=999999");
        Assert.Equal(3, clampedHigh.Objects.Count);
        Assert.False(clampedHigh.IsTruncated);
    }

    [Fact]
    public async Task StatsAggregateObjectsBytesAndTopLevelSplit()
    {
        var bucket = await CreateBucket("stats-test");
        await Put(bucket, "docs/a.txt", "hello world"); // 11
        await Put(bucket, "docs/b.txt", "abc");         // 3
        await Put(bucket, "readme.txt", "hi");          // 2

        var stats = await Stats(bucket, "");
        Assert.Equal(3, stats.Objects);
        Assert.Equal(16, stats.TotalBytes);
        Assert.Equal(1, stats.TopLevelFolders);
        Assert.Equal(1, stats.TopLevelFiles);

        var scoped = await Stats(bucket, "?prefix=docs/");
        Assert.Equal(2, scoped.Objects);
        Assert.Equal(14, scoped.TotalBytes);
        Assert.Equal(1, scoped.TopLevelFolders);
        Assert.Equal(0, scoped.TopLevelFiles);
    }

    [Fact]
    public async Task DeleteRemovesObjectFromIndex()
    {
        var bucket = await CreateBucket("delete-test");
        await Put(bucket, "keep.txt", "x");
        await Put(bucket, "gone.txt", "x");

        var del = new HttpRequestMessage(HttpMethod.Delete, $"/buckets/{bucket.Name}/objects/gone.txt");
        del.Headers.Authorization = Sign("DELETE", $"/buckets/{bucket.Name}/objects/gone.txt", bucket);
        Assert.Equal(HttpStatusCode.NoContent, (await _client.SendAsync(del)).StatusCode);

        var listed = await List(bucket, "");
        Assert.Equal(new[] { "keep.txt" }, listed.Objects.Select(o => o.Key));
        Assert.Equal(1, (await Stats(bucket, "")).Objects);
    }

    [Fact]
    public async Task AdminReindexRebuildsIndexFromDisk()
    {
        var bucket = await CreateBucket("reindex-test");
        await Put(bucket, "one", "x");
        await Put(bucket, "nested/two", "yy");
        await Put(bucket, "nested/three", "zzz");

        ClearIndexRows();

        var reindex = new HttpRequestMessage(HttpMethod.Post, $"/admin/buckets/{bucket.Name}/reindex");
        reindex.Headers.Add("X-Admin-Api-Key", AdminKey);
        var reindexResponse = await _client.SendAsync(reindex);
        Assert.Equal(HttpStatusCode.OK, reindexResponse.StatusCode);
        var body = await reindexResponse.Content.ReadFromJsonAsync<ReindexResponse>();
        Assert.Equal(3, body!.Indexed);

        var listed = await List(bucket, "");
        Assert.Equal(new[] { "nested/three", "nested/two", "one" }, listed.Objects.Select(o => o.Key));
        Assert.Equal(6, (await Stats(bucket, "")).TotalBytes);
    }

    [Fact]
    public async Task FirstListSelfHealsWhenIndexRowsAreMissing()
    {
        var bucket = await CreateBucket("selfheal-test");
        await Put(bucket, "alpha", "x");
        await Put(bucket, "beta", "x");

        ClearIndexRows();

        var listed = await List(bucket, "");
        Assert.Equal(new[] { "alpha", "beta" }, listed.Objects.Select(o => o.Key));
    }

    // ---- helpers ----

    private async Task<CreateBucketResponse> CreateBucket(string name)
    {
        var req = new HttpRequestMessage(HttpMethod.Post, "/admin/buckets")
        {
            Content = JsonContent.Create(new CreateBucketRequest(name)),
        };
        req.Headers.Add("X-Admin-Api-Key", AdminKey);
        var res = await _client.SendAsync(req);
        Assert.Equal(HttpStatusCode.Created, res.StatusCode);
        return (await res.Content.ReadFromJsonAsync<CreateBucketResponse>())!;
    }

    private async Task Put(CreateBucketResponse bucket, string key, string content)
    {
        var path = $"/buckets/{bucket.Name}/objects/{key}";
        var req = new HttpRequestMessage(HttpMethod.Put, path) { Content = new StringContent(content) };
        req.Headers.Authorization = Sign("PUT", path, bucket);
        Assert.Equal(HttpStatusCode.OK, (await _client.SendAsync(req)).StatusCode);
    }

    private async Task<ListObjectsResponse> List(CreateBucketResponse bucket, string query)
    {
        var path = $"/buckets/{bucket.Name}/objects";
        var req = new HttpRequestMessage(HttpMethod.Get, path + query);
        req.Headers.Authorization = Sign("GET", path, bucket);
        var res = await _client.SendAsync(req);
        Assert.Equal(HttpStatusCode.OK, res.StatusCode);
        return (await res.Content.ReadFromJsonAsync<ListObjectsResponse>())!;
    }

    private async Task<BucketStats> Stats(CreateBucketResponse bucket, string query)
    {
        var path = $"/buckets/{bucket.Name}/stats";
        var req = new HttpRequestMessage(HttpMethod.Get, path + query);
        req.Headers.Authorization = Sign("GET", path, bucket);
        var res = await _client.SendAsync(req);
        Assert.Equal(HttpStatusCode.OK, res.StatusCode);
        return (await res.Content.ReadFromJsonAsync<BucketStats>())!;
    }

    private void ClearIndexRows()
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<S3BenderDbContext>();
        db.Database.ExecuteSqlRaw("DELETE FROM \"Objects\"");
    }

    private static AuthenticationHeaderValue Sign(string method, string path, CreateBucketResponse bucket)
    {
        var timestamp = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        var stringToSign = $"{method}\n{path}\n{timestamp}";
        using var hmac = new HMACSHA256(Encoding.UTF8.GetBytes(bucket.SecretKey));
        var signature = Convert.ToHexString(hmac.ComputeHash(Encoding.UTF8.GetBytes(stringToSign))).ToLowerInvariant();
        return new AuthenticationHeaderValue("S3BENDER-HMAC-SHA256",
            $"AccessKey={bucket.AccessKey},Timestamp={timestamp},Signature={signature}");
    }
}
