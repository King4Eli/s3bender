using System.Net;
using System.Security.Cryptography;
using System.Text;
using Microsoft.Extensions.Options;
using S3Bender.Api.Dtos;
using S3Bender.Api.Options;

namespace S3Bender.Api.Middleware;

/// <summary>Guards every /admin/** route with a single shared secret (X-Admin-Api-Key).</summary>
public class AdminAuthMiddleware(RequestDelegate next)
{
    public const string Header = "X-Admin-Api-Key";

    public async Task InvokeAsync(HttpContext context, IOptions<S3BenderOptions> options)
    {
        if (!context.Request.Path.StartsWithSegments("/admin"))
        {
            await next(context);
            return;
        }

        var configured = options.Value.Auth.AdminApiKey;
        var provided = context.Request.Headers[Header].FirstOrDefault();

        if (string.IsNullOrWhiteSpace(configured))
        {
            await Reject(context, HttpStatusCode.InternalServerError, "AdminKeyNotConfigured", "Server admin API key is not configured");
            return;
        }

        if (provided is null || !CryptographicOperations.FixedTimeEquals(
                Encoding.UTF8.GetBytes(configured), Encoding.UTF8.GetBytes(provided)))
        {
            await Reject(context, HttpStatusCode.Unauthorized, "Unauthorized", $"Missing or invalid {Header} header");
            return;
        }

        await next(context);
    }

    private static async Task Reject(HttpContext context, HttpStatusCode status, string code, string message)
    {
        context.Response.StatusCode = (int)status;
        context.Response.ContentType = "application/json";
        await context.Response.WriteAsJsonAsync(ErrorResponse.Of(code, message));
    }
}
