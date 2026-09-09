using System.Security.Claims;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

/// <summary>
/// Minimal SuperAdmin JSON surface for staging automation (cookie Identity auth).
/// Does not introduce API keys or anonymous access.
/// </summary>
[ApiController]
[Route("api/v1/admin/nodes")]
[Authorize(Roles = AdminRole.SuperAdmin)]
[Produces("application/json")]
public sealed class AdminNodesController : ControllerBase
{
    private readonly INodeCommandService _commands;
    private readonly INodeManagementService _management;

    public AdminNodesController(INodeCommandService commands, INodeManagementService management)
    {
        _commands = commands;
        _management = management;
    }

    [HttpGet("{nodeId}")]
    public async Task<ActionResult<NodeAdminStatusResponse>> GetNode(
        string nodeId,
        CancellationToken cancellationToken)
    {
        try
        {
            var status = await _management.GetAdminStatusAsync(nodeId, cancellationToken)
                .ConfigureAwait(false);
            return Ok(status);
        }
        catch (NotFoundException)
        {
            return NotFound(new { error = "node_not_found" });
        }
    }

    public sealed class EnqueueCommandRequest
    {
        public string Type { get; set; } = "";
    }

    [HttpPost("{nodeId}/commands")]
    public async Task<ActionResult<object>> EnqueueCommand(
        string nodeId,
        [FromBody] EnqueueCommandRequest body,
        CancellationToken cancellationToken)
    {
        if (!Enum.TryParse<NodeCommandType>(body.Type, ignoreCase: true, out var type))
            return BadRequest(new { error = "invalid_type" });

        var actor = User.FindFirstValue(ClaimTypes.Email)
                    ?? User.Identity?.Name
                    ?? "admin-api";
        var roles = User.FindAll(ClaimTypes.Role).Select(c => c.Value);

        try
        {
            var cmd = await _commands.EnqueueAsync(nodeId, type, actor, roles, cancellationToken)
                .ConfigureAwait(false);
            return Ok(new
            {
                id = cmd.Id,
                node_id = cmd.NodeId,
                type = cmd.Type.ToString(),
                status = cmd.Status.ToString(),
                target_version = cmd.TargetVersion,
                payload_json = cmd.PayloadJson
            });
        }
        catch (ConflictException ex)
        {
            return Conflict(new { error = "conflict", message = ex.Message });
        }
        catch (ForbiddenException ex)
        {
            return StatusCode(StatusCodes.Status403Forbidden, new { error = "forbidden", message = ex.Message });
        }
        catch (NotFoundException)
        {
            return NotFound(new { error = "node_not_found" });
        }
        catch (ValidationException ex)
        {
            return BadRequest(new { error = "validation", message = ex.Message });
        }
    }

    public sealed class DrainingRequest
    {
        public bool Draining { get; set; }
    }

    [HttpPost("{nodeId}/draining")]
    public async Task<IActionResult> SetDraining(
        string nodeId,
        [FromBody] DrainingRequest body,
        CancellationToken cancellationToken)
    {
        var actor = User.FindFirstValue(ClaimTypes.Email)
                    ?? User.Identity?.Name
                    ?? "admin-api";
        try
        {
            await _management.SetDrainingAsync(nodeId, body.Draining, actor, cancellationToken)
                .ConfigureAwait(false);
            return Ok(new { node_id = nodeId, draining = body.Draining });
        }
        catch (NotFoundException)
        {
            return NotFound(new { error = "node_not_found" });
        }
        catch (ValidationException ex)
        {
            return BadRequest(new { error = "validation", message = ex.Message });
        }
    }
}
