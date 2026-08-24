using S3Bender.Api.Dtos;

namespace S3Bender.Api.Middleware;

public class ExceptionHandlingMiddleware(RequestDelegate next, ILogger<ExceptionHandlingMiddleware> logger)
{
    public async Task InvokeAsync(HttpContext context)
    {
        try
        {
            await next(context);
        }
        catch (ApiException ex)
        {
            context.Response.StatusCode = (int)ex.Status;
            context.Response.ContentType = "application/json";
            await context.Response.WriteAsJsonAsync(ErrorResponse.Of(ex.Code, ex.Message));
        }
        catch (Exception ex)
        {
            logger.LogError(ex, "Unhandled exception");
            context.Response.StatusCode = 500;
            context.Response.ContentType = "application/json";
            await context.Response.WriteAsJsonAsync(ErrorResponse.Of("InternalError", "An unexpected error occurred"));
        }
    }
}
