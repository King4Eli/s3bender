using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.DependencyInjection;
using S3Bender.Api;
using S3Bender.Api.Dtos;
using Xunit;

namespace S3Bender.Api.Tests;

/// <summary>
/// End-to-end: create a bucket, upload an object with the bucket's own HMAC credentials, fetch
/// it back, then fetch it again through a presigned URL with no Authorization header at all.
/// Mirrors engine/src/test/java/com/s3bender/api/BucketFlowIntegrationTest.java.
/// </summary>
public class BucketFlowTests : IClassFixture<WebApplicationFactory<Program>>, IDisposable
{
    private const string AdminKey = "test-admin-key";
    private readonly string _tempDir = Directory.CreateTempSubdirectory("s3bender-test-").FullName;
    private readonly HttpClient _client;

    public BucketFlowTests(WebApplicationFactory<Program> factory)
    {
        Environment.SetEnvironmentVariable("S3BENDER_ADMIN_API_KEY", AdminKey);
        Environment.SetEnvironmentVariable("S3BENDER_MASTER_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=");
        Environment.SetEnvironmentVariable("S3BENDER_STORAGE_ROOT", Path.Combine(_tempDir, "objects"));
        Environment.SetEnvironmentVariable("S3BENDER_DB_PATH", Path.Combine(_tempDir, "s3bender.db"));

        _client = factory.CreateClient();
    }

    public void Dispose()
    {
        try { Directory.Delete(_tempDir, recursive: true); } catch { /* best-effort cleanup */ }
    }

    [Fact]
    public async Task CreateUploadDownloadAndPresign()
    {
        const string bucketName = "flow-test-bucket";

        var createRequest = new HttpRequestMessage(HttpMethod.Post, "/admin/buckets")
        {
            Content = JsonContent.Create(new CreateBucketRequest(bucketName)),
        };
        createRequest.Headers.Add("X-Admin-Api-Key", AdminKey);
        var createResponse = await _client.SendAsync(createRequest);
        Assert.Equal(HttpStatusCode.Created, createResponse.StatusCode);
        var bucket = await createResponse.Content.ReadFromJsonAsync<CreateBucketResponse>();
        Assert.NotNull(bucket);

        const string objectKey = "docs/hello.txt";
        const string content = "hello s3bender";
        var path = $"/buckets/{bucketName}/objects/{objectKey}";

        var putRequest = new HttpRequestMessage(HttpMethod.Put, path) { Content = new StringContent(content) };
        putRequest.Headers.Authorization = SignHeader("PUT", path, bucket!.SecretKey, bucket.AccessKey);
        var putResponse = await _client.SendAsync(putRequest);
        Assert.Equal(HttpStatusCode.OK, putResponse.StatusCode);

        var getRequest = new HttpRequestMessage(HttpMethod.Get, path);
        getRequest.Headers.Authorization = SignHeader("GET", path, bucket.SecretKey, bucket.AccessKey);
        var getResponse = await _client.SendAsync(getRequest);
        Assert.Equal(HttpStatusCode.OK, getResponse.StatusCode);
        Assert.Equal(content, await getResponse.Content.ReadAsStringAsync());

        var presignPath = $"/buckets/{bucketName}/presign";
        var presignRequest = new HttpRequestMessage(HttpMethod.Post, presignPath)
        {
            Content = JsonContent.Create(new PresignRequest(objectKey, "GET", 60)),
        };
        presignRequest.Headers.Authorization = SignHeader("POST", presignPath, bucket.SecretKey, bucket.AccessKey);
        var presignResponse = await _client.SendAsync(presignRequest);
        Assert.Equal(HttpStatusCode.OK, presignResponse.StatusCode);
        var presign = await presignResponse.Content.ReadFromJsonAsync<PresignResponse>();
        Assert.NotNull(presign);

        var presignedGet = await _client.GetAsync(new Uri(presign!.Url).PathAndQuery);
        Assert.Equal(HttpStatusCode.OK, presignedGet.StatusCode);
        Assert.Equal(content, await presignedGet.Content.ReadAsStringAsync());
    }

    [Fact]
    public async Task RotateKeyInvalidatesOldCredentialsAndKeepsObjects()
    {
        const string bucketName = "rotate-test-bucket";

        var createRequest = new HttpRequestMessage(HttpMethod.Post, "/admin/buckets")
        {
            Content = JsonContent.Create(new CreateBucketRequest(bucketName)),
        };
        createRequest.Headers.Add("X-Admin-Api-Key", AdminKey);
        var original = await (await _client.SendAsync(createRequest)).Content.ReadFromJsonAsync<CreateBucketResponse>();
        Assert.NotNull(original);

        const string objectKey = "keep-me.txt";
        var path = $"/buckets/{bucketName}/objects/{objectKey}";
        var putRequest = new HttpRequestMessage(HttpMethod.Put, path) { Content = new StringContent("still here") };
        putRequest.Headers.Authorization = SignHeader("PUT", path, original!.SecretKey, original.AccessKey);
        Assert.Equal(HttpStatusCode.OK, (await _client.SendAsync(putRequest)).StatusCode);

        var rotateRequest = new HttpRequestMessage(HttpMethod.Post, $"/admin/buckets/{bucketName}/rotate");
        rotateRequest.Headers.Add("X-Admin-Api-Key", AdminKey);
        var rotateResponse = await _client.SendAsync(rotateRequest);
        Assert.Equal(HttpStatusCode.OK, rotateResponse.StatusCode);
        var rotated = await rotateResponse.Content.ReadFromJsonAsync<CreateBucketResponse>();
        Assert.NotNull(rotated);
        Assert.NotEqual(original.AccessKey, rotated!.AccessKey);
        Assert.NotEqual(original.SecretKey, rotated.SecretKey);

        var oldCredsRequest = new HttpRequestMessage(HttpMethod.Get, path);
        oldCredsRequest.Headers.Authorization = SignHeader("GET", path, original.SecretKey, original.AccessKey);
        Assert.Equal(HttpStatusCode.Unauthorized, (await _client.SendAsync(oldCredsRequest)).StatusCode);

        var newCredsRequest = new HttpRequestMessage(HttpMethod.Get, path);
        newCredsRequest.Headers.Authorization = SignHeader("GET", path, rotated.SecretKey, rotated.AccessKey);
        var newCredsResponse = await _client.SendAsync(newCredsRequest);
        Assert.Equal(HttpStatusCode.OK, newCredsResponse.StatusCode);
        Assert.Equal("still here", await newCredsResponse.Content.ReadAsStringAsync());
    }

    private static AuthenticationHeaderValue SignHeader(string method, string path, string secret, string accessKey)
    {
        var timestamp = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        var stringToSign = $"{method}\n{path}\n{timestamp}";
        using var hmac = new HMACSHA256(Encoding.UTF8.GetBytes(secret));
        var signature = Convert.ToHexString(hmac.ComputeHash(Encoding.UTF8.GetBytes(stringToSign))).ToLowerInvariant();
        return new AuthenticationHeaderValue("S3BENDER-HMAC-SHA256",
            $"AccessKey={accessKey},Timestamp={timestamp},Signature={signature}");
    }
}
