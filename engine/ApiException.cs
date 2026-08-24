using System.Net;

namespace S3Bender.Api;

public class ApiException : Exception
{
    public HttpStatusCode Status { get; }
    public string Code { get; }

    public ApiException(HttpStatusCode status, string code, string message) : base(message)
    {
        Status = status;
        Code = code;
    }

    public static ApiException BadRequest(string code, string message) => new(HttpStatusCode.BadRequest, code, message);
    public static ApiException NotFound(string code, string message) => new(HttpStatusCode.NotFound, code, message);
    public static ApiException Conflict(string code, string message) => new(HttpStatusCode.Conflict, code, message);
    public static ApiException Unauthorized(string code, string message) => new(HttpStatusCode.Unauthorized, code, message);
}
