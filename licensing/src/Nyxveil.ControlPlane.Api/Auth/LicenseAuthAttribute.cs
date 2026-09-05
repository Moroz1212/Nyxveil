using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Filters;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;

namespace Nyxveil.ControlPlane.Api.Auth;

/// <summary>
/// Requires a valid license token (Bearer {id:secret} or X-License-Token)
/// or a valid access ticket JWT in Authorization Bearer.
/// </summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Method)]
public sealed class LicenseAuthAttribute : Attribute, IAsyncActionFilter
{
    public async Task OnActionExecutionAsync(ActionExecutingContext context, ActionExecutionDelegate next)
    {
        var http = context.HttpContext;
        var token = AuthTokenExtractor.ExtractLicenseOrBearerToken(http.Request);
        if (string.IsNullOrWhiteSpace(token))
        {
            context.Result = new UnauthorizedObjectResult(Problem("unauthorized", StatusCodes.Status401Unauthorized));
            return;
        }

        try
        {
            if (AuthTokenExtractor.LooksLikeLicenseToken(token))
            {
                var licenses = http.RequestServices.GetRequiredService<ILicenseProvisioningService>();
                var result = await licenses.ValidateLicenseTokenAsync(
                        new LicenseValidateRequest { LicenseToken = token },
                        http.RequestAborted)
                    .ConfigureAwait(false);

                if (!result.Valid)
                {
                    context.Result = new UnauthorizedObjectResult(Problem("unauthorized", StatusCodes.Status401Unauthorized));
                    return;
                }

                http.Items[AuthContextKeys.LicenseToken] = token;
                http.Items[AuthContextKeys.AuthKind] = AuthContextKeys.AuthKindLicense;
            }
            else
            {
                var tickets = http.RequestServices.GetRequiredService<IAccessTicketIssuer>();
                AccessTicketClaims claims;
                try
                {
                    claims = tickets.VerifyAccessTicket(token);
                }
                catch (Exception)
                {
                    context.Result = new UnauthorizedObjectResult(Problem("unauthorized", StatusCodes.Status401Unauthorized));
                    return;
                }

                if (claims.ExpiresAt is long exp)
                {
                    var now = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
                    if (exp < now)
                    {
                        context.Result = new UnauthorizedObjectResult(Problem("unauthorized", StatusCodes.Status401Unauthorized));
                        return;
                    }
                }

                http.Items[AuthContextKeys.TicketClaims] = claims;
                http.Items[AuthContextKeys.AuthKind] = AuthContextKeys.AuthKindTicket;
            }
        }
        catch (UnauthorizedException)
        {
            context.Result = new UnauthorizedObjectResult(Problem("unauthorized", StatusCodes.Status401Unauthorized));
            return;
        }
        catch (ForbiddenException ex)
        {
            context.Result = new ObjectResult(Problem(ex.Message, StatusCodes.Status403Forbidden))
            {
                StatusCode = StatusCodes.Status403Forbidden
            };
            return;
        }

        await next().ConfigureAwait(false);
    }

    private static ProblemDetails Problem(string detail, int status) => new()
    {
        Title = status switch
        {
            StatusCodes.Status401Unauthorized => "Unauthorized",
            StatusCodes.Status403Forbidden => "Forbidden",
            _ => "Error"
        },
        Detail = detail,
        Status = status
    };
}
