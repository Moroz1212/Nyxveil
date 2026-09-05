using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Filters;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Exceptions;

namespace Nyxveil.ControlPlane.Api.Auth;

/// <summary>
/// Requires node service identity via X-Node-Id + Authorization Bearer (or signature headers).
/// User license tokens are rejected with 403.
/// </summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Method)]
public sealed class NodeAuthAttribute : Attribute, IAsyncActionFilter
{
    /// <summary>
    /// When true, also accepts <c>node_id</c> / <c>node_token</c> from the bound action argument
    /// (Core PathNodeHealth body style).
    /// </summary>
    public bool AllowBodyCredentials { get; set; }

    public async Task OnActionExecutionAsync(ActionExecutingContext context, ActionExecutionDelegate next)
    {
        var http = context.HttpContext;
        var nodeId = AuthTokenExtractor.ExtractNodeId(http.Request);
        var bearer = AuthTokenExtractor.ExtractBearer(http.Request);

        if (string.IsNullOrWhiteSpace(bearer))
            bearer = http.Request.Query["node_token"].ToString();

        if (string.IsNullOrWhiteSpace(nodeId))
            nodeId = ResolveRouteNodeId(context);

        if (AllowBodyCredentials)
            TryApplyBodyCredentials(context, ref nodeId, ref bearer);

        if (string.IsNullOrWhiteSpace(nodeId) || string.IsNullOrWhiteSpace(bearer))
        {
            // Allow signature-only auth without bearer when X-Node-Signature is present.
            var hasSig = !string.IsNullOrWhiteSpace(http.Request.Headers["X-Node-Signature"].ToString());
            if (string.IsNullOrWhiteSpace(nodeId) || (!hasSig && string.IsNullOrWhiteSpace(bearer)))
            {
                context.Result = new UnauthorizedObjectResult(Problem("unauthorized", StatusCodes.Status401Unauthorized));
                return;
            }
        }

        if (!string.IsNullOrWhiteSpace(bearer) && AuthTokenExtractor.LooksLikeLicenseToken(bearer))
        {
            context.Result = new ObjectResult(Problem("forbidden", StatusCodes.Status403Forbidden))
            {
                StatusCode = StatusCodes.Status403Forbidden
            };
            return;
        }

        var headers = NodeAuthHeaderBuilder.Build(http.Request, bearer);
        var authenticator = http.RequestServices.GetRequiredService<INodeAuthenticator>();

        try
        {
            await authenticator.ValidateNodeRequestAsync(nodeId!, headers, http.RequestAborted)
                .ConfigureAwait(false);
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

        http.Items[AuthContextKeys.NodeId] = nodeId;
        http.Items[AuthContextKeys.AuthKind] = AuthContextKeys.AuthKindNode;
        await next().ConfigureAwait(false);
    }

    private static string? ResolveRouteNodeId(ActionExecutingContext context)
    {
        if (context.RouteData.Values.TryGetValue("nodeId", out var v) && v is string s && !string.IsNullOrWhiteSpace(s))
            return s;
        if (context.RouteData.Values.TryGetValue("node_id", out v) && v is string s2 && !string.IsNullOrWhiteSpace(s2))
            return s2;
        return null;
    }

    private static void TryApplyBodyCredentials(
        ActionExecutingContext context,
        ref string? nodeId,
        ref string? bearer)
    {
        foreach (var arg in context.ActionArguments.Values)
        {
            if (arg is null)
                continue;

            var type = arg.GetType();
            var nodeIdProp = type.GetProperty("NodeId");
            var tokenProp = type.GetProperty("NodeToken");
            if (nodeIdProp is null && tokenProp is null)
                continue;

            if (string.IsNullOrWhiteSpace(nodeId) && nodeIdProp?.GetValue(arg) is string nid && !string.IsNullOrWhiteSpace(nid))
                nodeId = nid;
            if (string.IsNullOrWhiteSpace(bearer) && tokenProp?.GetValue(arg) is string tok && !string.IsNullOrWhiteSpace(tok))
                bearer = tok;
        }
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

internal static class NodeAuthHeaderBuilder
{
    public static Dictionary<string, string> Build(HttpRequest request, string? bearer)
    {
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

        foreach (var header in request.Headers)
        {
            if (header.Key.StartsWith("X-Node-", StringComparison.OrdinalIgnoreCase) ||
                string.Equals(header.Key, "Authorization", StringComparison.OrdinalIgnoreCase))
            {
                headers[header.Key] = header.Value.ToString();
            }
        }

        if (!string.IsNullOrWhiteSpace(bearer) &&
            !headers.ContainsKey("Authorization"))
        {
            headers["Authorization"] = "Bearer " + bearer;
        }

        if (!headers.ContainsKey("X-Node-Method"))
            headers["X-Node-Method"] = request.Method;
        if (!headers.ContainsKey("X-Node-Path"))
            headers["X-Node-Path"] = request.Path.Value ?? "/";

        return headers;
    }
}
