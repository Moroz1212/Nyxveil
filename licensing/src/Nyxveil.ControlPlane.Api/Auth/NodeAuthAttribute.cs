using System.Security.Cryptography;
using System.Text.Json;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Filters;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Exceptions;

namespace Nyxveil.ControlPlane.Api.Auth;

/// <summary>Checks req-v2 before model binding, preserving exact body bytes.</summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Method)]
public sealed class NodeAuthAttribute : Attribute, IAsyncResourceFilter
{
    public const int MaxBodyBytes = 64 * 1024;

    public async Task OnResourceExecutionAsync(ResourceExecutingContext context, ResourceExecutionDelegate next)
    {
        var http = context.HttpContext;
        var request = http.Request;
        var nodeIds = request.Headers["X-Node-Id"];
        var nodeId = nodeIds.Count == 1 ? nodeIds[0] : null;
        if (AuthTokenExtractor.LooksLikeLicenseToken(AuthTokenExtractor.ExtractBearer(request) ?? string.Empty))
        {
            context.Result = new StatusCodeResult(StatusCodes.Status403Forbidden);
            return;
        }
        try
        {
            if (string.IsNullOrWhiteSpace(nodeId))
                throw new UnauthorizedException("missing node identity");
            foreach (var pair in context.RouteData.Values.Where(p => IsNodeId(p.Key)))
                CheckIdentity(nodeId, pair.Value?.ToString());
            foreach (var pair in request.Query.Where(p => IsNodeId(p.Key)))
                foreach (var value in pair.Value)
                    CheckIdentity(nodeId, value);

            if (request.ContentLength > MaxBodyBytes)
            {
                context.Result = new StatusCodeResult(StatusCodes.Status413PayloadTooLarge);
                return;
            }
            request.EnableBuffering();
            var body = new byte[MaxBodyBytes + 1];
            var length = 0;
            try
            {
                while (length < body.Length)
                {
                    var read = await request.Body.ReadAsync(body.AsMemory(length), http.RequestAborted);
                    if (read == 0) break;
                    length += read;
                }
            }
            finally { request.Body.Position = 0; }
            if (length > MaxBodyBytes)
            {
                context.Result = new StatusCodeResult(StatusCodes.Status413PayloadTooLarge);
                return;
            }
            if (length > 0)
            {
                using var json = JsonDocument.Parse(body.AsMemory(0, length));
                if (json.RootElement.ValueKind != JsonValueKind.Object)
                    throw new UnauthorizedException("expected object body");
                // Include duplicate properties and all NodeId DTO casing aliases.
                foreach (var property in json.RootElement.EnumerateObject().Where(p => IsNodeId(p.Name)))
                    CheckIdentity(nodeId, property.Value.ValueKind == JsonValueKind.String ? property.Value.GetString() : null);
            }
            var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            foreach (var name in new[] { "X-Node-Timestamp", "X-Node-Nonce", "X-Node-Signature" })
            {
                var values = request.Headers[name];
                if (values.Count != 1) throw new UnauthorizedException("missing or duplicate signature header");
                headers[name] = values[0]!;
            }
            var actual = new NodeRequestData(request.Method,
                request.PathBase.ToUriComponent() + request.Path.ToUriComponent() + request.QueryString.ToUriComponent(),
                Convert.ToHexString(SHA256.HashData(body.AsSpan(0, length))).ToLowerInvariant());
            await http.RequestServices.GetRequiredService<INodeAuthenticator>()
                .ValidateNodeRequestAsync(nodeId, headers, actual, http.RequestAborted);
        }
        catch (ForbiddenException)
        {
            context.Result = new StatusCodeResult(StatusCodes.Status403Forbidden);
            return;
        }
        catch (Exception ex) when (ex is UnauthorizedException or JsonException)
        {
            context.Result = new UnauthorizedResult();
            return;
        }
        http.Items[AuthContextKeys.NodeId] = nodeId;
        http.Items[AuthContextKeys.AuthKind] = AuthContextKeys.AuthKindNode;
        await next();
    }

    private static bool IsNodeId(string name) =>
        name.Equals("node_id", StringComparison.OrdinalIgnoreCase) || name.Equals("NodeId", StringComparison.OrdinalIgnoreCase);

    private static void CheckIdentity(string authenticated, string? supplied)
    {
        if (!string.Equals(authenticated, supplied, StringComparison.Ordinal))
            throw new ForbiddenException("node identity mismatch");
    }
}
