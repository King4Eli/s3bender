using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.ModelBinding;
using Microsoft.Data.Sqlite;
using Microsoft.EntityFrameworkCore;
using S3Bender.Api.Data;
using S3Bender.Api.Dtos;
using S3Bender.Api.Middleware;
using S3Bender.Api.Options;
using S3Bender.Api.Services;

var builder = WebApplication.CreateBuilder(args);

var apiPort = int.Parse(Environment.GetEnvironmentVariable("PORT") ?? "8080");
var frontendPort = int.Parse(Environment.GetEnvironmentVariable("S3BENDER_FRONTEND_PORT") ?? "8081");
// Both ports answer the same app/pipeline - the console UI (wwwroot/) is reachable on either,
// same-origin, no separate proxy service needed. See the port-scope middleware below for the one
// thing that's deliberately one-directional.
builder.WebHost.UseUrls($"http://0.0.0.0:{apiPort}", $"http://0.0.0.0:{frontendPort}");

builder.Services.Configure<S3BenderOptions>(options =>
{
    options.Storage.Root = Environment.GetEnvironmentVariable("S3BENDER_STORAGE_ROOT") ?? "./data/objects";
    options.Auth.AdminApiKey = Environment.GetEnvironmentVariable("S3BENDER_ADMIN_API_KEY");
    options.Auth.MasterKey = Environment.GetEnvironmentVariable("S3BENDER_MASTER_KEY");
    options.PublicBaseUrl = Environment.GetEnvironmentVariable("S3BENDER_PUBLIC_BASE_URL");
    options.Signing.ClockSkewSeconds =
        long.TryParse(Environment.GetEnvironmentVariable("S3BENDER_CLOCK_SKEW_SECONDS"), out var skew) ? skew : 900;
    options.Signing.MaxPresignExpirySeconds =
        long.TryParse(Environment.GetEnvironmentVariable("S3BENDER_MAX_PRESIGN_EXPIRY_SECONDS"), out var maxExpiry) ? maxExpiry : 604800;
});

var dbPath = Environment.GetEnvironmentVariable("S3BENDER_DB_PATH") ?? "./data/db/s3bender.db";
var fullDbPath = Path.GetFullPath(dbPath);
Directory.CreateDirectory(Path.GetDirectoryName(fullDbPath)!);
builder.Services.AddDbContext<S3BenderDbContext>(opts => opts.UseSqlite($"Data Source={fullDbPath}"));

builder.Services.AddSingleton<CryptoService>();
builder.Services.AddSingleton<SignatureService>();
builder.Services.AddSingleton<ObjectStorageService>();
builder.Services.AddScoped<BucketService>();
builder.Services.AddScoped<PresignService>();

builder.Services.AddControllers(options =>
{
    // Object PUT bodies are raw bytes, never a form - MVC's form value provider factories would
    // otherwise eagerly call Request.ReadFormAsync() (and so consume the request body itself)
    // for ANY request with Content-Type: application/x-www-form-urlencoded, even though no
    // action here has a [FromForm] parameter to justify it.
    foreach (var factory in options.ValueProviderFactories
                 .Where(f => f is FormValueProviderFactory or JQueryFormValueProviderFactory).ToList())
    {
        options.ValueProviderFactories.Remove(factory);
    }
}).ConfigureApiBehaviorOptions(opts =>
{
    opts.InvalidModelStateResponseFactory = context =>
    {
        var message = context.ModelState.Values.SelectMany(v => v.Errors).FirstOrDefault()?.ErrorMessage ?? "Invalid request";
        return new BadRequestObjectResult(ErrorResponse.Of("InvalidRequest", message));
    };
});

var app = builder.Build();

using (var scope = app.Services.CreateScope())
{
    var database = scope.ServiceProvider.GetRequiredService<S3BenderDbContext>().Database;
    database.EnsureCreated();
    // EnsureCreated() only ever creates a brand-new schema - it never alters an existing one. For
    // columns added after a DB was first created, patch them in by hand; SQLite ADD COLUMN is a
    // cheap metadata-only change. (This project deliberately doesn't carry EF migrations.)
    foreach (var ddl in new[]
             {
                 "ALTER TABLE \"Buckets\" ADD COLUMN \"Description\" TEXT NULL",
             })
    {
        try { database.ExecuteSqlRaw(ddl); }
        catch (SqliteException) { /* column already present - fresh DBs get it from EnsureCreated */ }
    }
}

// Eagerly resolve so a missing/invalid S3BENDER_MASTER_KEY fails startup immediately, rather than
// on the first bucket-scoped request - fail closed, matching the Java engine's @PostConstruct check.
app.Services.GetRequiredService<CryptoService>();

// PUT/POST bodies must be readable in the controller regardless of what else in the pipeline
// (MVC's own model binding/validation infrastructure inspects Content-Type broadly - see the
// ValueProviderFactories removal above, which alone wasn't sufficient) may have already peeked
// at the request stream. Buffering makes it seekable so the controller can rewind to the start.
app.Use(async (context, next) =>
{
    context.Request.EnableBuffering();
    await next();
});

app.UseMiddleware<ExceptionHandlingMiddleware>();
app.UseMiddleware<AdminAuthMiddleware>();
app.UseMiddleware<BucketAuthMiddleware>();

// The console's own static assets 404 on the plain API port - external API clients have no use
// for them. Every real API route (/admin/**, /buckets/**, /healthz) deliberately stays reachable
// on BOTH ports: the console's own JS calls the full API same-origin from whichever port served
// the page (it has a full Admin panel, not just the object browser), so blocking anything there
// would break the built-in UI, not just "unnecessarily expose" something.
var consoleAssetPaths = new HashSet<string> { "/", "/index.html", "/app.js", "/styles.css" };
app.Use(async (context, next) =>
{
    if (context.Connection.LocalPort == apiPort && consoleAssetPaths.Contains(context.Request.Path.Value ?? ""))
    {
        context.Response.StatusCode = 404;
        context.Response.ContentType = "application/json";
        await context.Response.WriteAsJsonAsync(ErrorResponse.Of("NotFound", "No such route on this port"));
        return;
    }
    await next();
});

app.UseDefaultFiles();
app.UseStaticFiles();
app.MapControllers();

app.Run();

namespace S3Bender.Api
{
    /// <summary>Marker so WebApplicationFactory&lt;Program&gt; can be used from Api.Tests.</summary>
    public partial class Program;
}
