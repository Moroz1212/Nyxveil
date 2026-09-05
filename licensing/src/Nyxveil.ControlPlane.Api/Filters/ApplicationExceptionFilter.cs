using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Filters;
using AppEx = Nyxveil.ControlPlane.Application.Exceptions;

namespace Nyxveil.ControlPlane.Api.Filters;

/// <summary>
/// Maps Application-layer exceptions to RFC 7807 ProblemDetails responses.
/// </summary>
public sealed class ApplicationExceptionFilter : IExceptionFilter
{
    public void OnException(ExceptionContext context)
    {
        if (context.ExceptionHandled)
            return;

        var (status, title) = Map(context.Exception);
        if (status is null)
            return;

        var problem = new ProblemDetails
        {
            Title = title,
            Detail = context.Exception.Message,
            Status = status,
            Instance = context.HttpContext.Request.Path
        };

        if (context.Exception is AppEx.ValidationException ve && ve.Errors.Count > 0)
            problem.Extensions["errors"] = ve.Errors;

        context.Result = new ObjectResult(problem)
        {
            StatusCode = status,
            ContentTypes = { "application/problem+json" }
        };
        context.ExceptionHandled = true;
    }

    private static (int? Status, string? Title) Map(Exception exception) => exception switch
    {
        AppEx.NotFoundException => (StatusCodes.Status404NotFound, "Not Found"),
        AppEx.ConflictException => (StatusCodes.Status409Conflict, "Conflict"),
        AppEx.ForbiddenException => (StatusCodes.Status403Forbidden, "Forbidden"),
        AppEx.UnauthorizedException => (StatusCodes.Status401Unauthorized, "Unauthorized"),
        AppEx.ValidationException => (StatusCodes.Status400BadRequest, "Validation Failed"),
        AppEx.ApplicationException => (StatusCodes.Status400BadRequest, "Bad Request"),
        _ => (null, null)
    };
}
