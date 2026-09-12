using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1/node/commands")]
[Produces("application/json")]
public sealed class NodeCommandsController : ControllerBase
{
    private readonly INodeCommandService _commands;

    public NodeCommandsController(INodeCommandService commands)
    {
        _commands = commands;
    }

    /// <summary>GET /api/v1/node/commands/next — claim next pending command (or 204).</summary>
    [HttpGet("next")]
    [NodeAuth]
    [RateLimit]
    public async Task<ActionResult<NodeCommandDto>> ClaimNext(CancellationToken cancellationToken)
    {
        var nodeId = AuthTokenExtractor.GetNodeId(HttpContext)
                     ?? throw new InvalidOperationException("node id missing after NodeAuth");

        var command = await _commands.ClaimNextAsync(nodeId, cancellationToken).ConfigureAwait(false);
        if (command is null)
            return NoContent();

        return Ok(ToDto(command));
    }

    /// <summary>POST /api/v1/node/commands/{id}/started</summary>
    [HttpPost("{id:guid}/started")]
    [NodeAuth]
    [RateLimit]
    public async Task<IActionResult> MarkStarted(Guid id, CancellationToken cancellationToken)
    {
        var nodeId = AuthTokenExtractor.GetNodeId(HttpContext)
                     ?? throw new InvalidOperationException("node id missing after NodeAuth");

        await _commands.MarkStartedAsync(id, nodeId, cancellationToken).ConfigureAwait(false);
        return NoContent();
    }

    /// <summary>POST /api/v1/node/commands/{id}/progress — refresh execution lease / phase.</summary>
    [HttpPost("{id:guid}/progress")]
    [NodeAuth]
    [RateLimit]
    public async Task<IActionResult> ReportProgress(
        Guid id,
        [FromBody] NodeCommandProgressRequest request,
        CancellationToken cancellationToken)
    {
        var nodeId = AuthTokenExtractor.GetNodeId(HttpContext)
                     ?? throw new InvalidOperationException("node id missing after NodeAuth");

        await _commands.ReportProgressAsync(
                id,
                nodeId,
                request.Phase,
                request.Message,
                cancellationToken)
            .ConfigureAwait(false);
        return NoContent();
    }

    /// <summary>POST /api/v1/node/commands/{id}/result</summary>
    [HttpPost("{id:guid}/result")]
    [NodeAuth]
    [RateLimit]
    public async Task<IActionResult> Complete(
        Guid id,
        [FromBody] NodeCommandResultRequest request,
        CancellationToken cancellationToken)
    {
        var nodeId = AuthTokenExtractor.GetNodeId(HttpContext)
                     ?? throw new InvalidOperationException("node id missing after NodeAuth");

        await _commands.CompleteAsync(
                id,
                nodeId,
                request.Success,
                request.ResultCode,
                request.ResultMessage,
                request.BootId,
                cancellationToken)
            .ConfigureAwait(false);
        return NoContent();
    }

    private static NodeCommandDto ToDto(NodeCommand command) => new()
    {
        Id = command.Id,
        NodeId = command.NodeId,
        Type = command.Type.ToString(),
        Status = command.Status.ToString(),
        IssuedAt = command.IssuedAt,
        ExpiresAt = command.ExpiresAt,
        CorrelationId = command.CorrelationId,
        PayloadJson = command.PayloadJson,
        PreviousVersion = command.PreviousVersion,
        TargetVersion = command.TargetVersion
    };
}
