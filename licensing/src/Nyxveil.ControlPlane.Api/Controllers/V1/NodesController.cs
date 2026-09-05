using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class NodesController : ControllerBase
{
    private readonly INodeRegistrationService _registration;
    private readonly INodeHeartbeatService _heartbeat;
    private readonly ISigningKeyService _signingKeys;
    private readonly SigningOptions _signing;

    public NodesController(
        INodeRegistrationService registration,
        INodeHeartbeatService heartbeat,
        ISigningKeyService signingKeys,
        IOptions<SigningOptions> signing)
    {
        _registration = registration;
        _heartbeat = heartbeat;
        _signingKeys = signingKeys;
        _signing = signing.Value;
    }

    /// <summary>
    /// POST /api/v1/nodes/register — bootstrap token in body (not node auth).
    /// </summary>
    [HttpPost("nodes/register")]
    [RateLimit]
    public async Task<ActionResult<NodeRegisterResponse>> Register(
        [FromBody] NodeRegisterRequest request,
        CancellationToken cancellationToken)
    {
        var result = await _registration.RegisterWithBootstrapAsync(request, cancellationToken)
            .ConfigureAwait(false);
        return Ok(result);
    }

    /// <summary>
    /// POST /api/v1/nodes/{nodeId}/health — Core PathNodeHealth; requires node auth
    /// (nvp-node-req-v2 signature headers).
    /// </summary>
    [HttpPost("nodes/{nodeId}/health")]
    [NodeAuth]
    [RateLimit]
    public async Task<ActionResult<NodeHeartbeatResponse>> Health(
        string nodeId,
        [FromBody] NodeHeartbeatRequest request,
        CancellationToken cancellationToken)
    {
        request.NodeId = string.IsNullOrWhiteSpace(request.NodeId) ? nodeId : request.NodeId;
        if (!string.Equals(request.NodeId, nodeId, StringComparison.Ordinal))
            return BadRequest(new ProblemDetails
            {
                Title = "Validation Failed",
                Detail = "node_id path/body mismatch",
                Status = StatusCodes.Status400BadRequest
            });

        var result = await _heartbeat.ProcessHeartbeatAsync(request, cancellationToken)
            .ConfigureAwait(false);
        return Ok(result);
    }

    /// <summary>
    /// POST /api/v1/node/heartbeat — convenience alias for PathNodeHealth.
    /// </summary>
    [HttpPost("node/heartbeat")]
    [NodeAuth]
    [RateLimit]
    public async Task<ActionResult<NodeHeartbeatResponse>> Heartbeat(
        [FromBody] NodeHeartbeatRequest request,
        CancellationToken cancellationToken)
    {
        var authNodeId = AuthTokenExtractor.GetNodeId(HttpContext);
        if (string.IsNullOrWhiteSpace(request.NodeId))
            request.NodeId = authNodeId ?? string.Empty;

        if (!string.IsNullOrWhiteSpace(authNodeId) &&
            !string.Equals(request.NodeId, authNodeId, StringComparison.Ordinal))
        {
            return BadRequest(new ProblemDetails
            {
                Title = "Validation Failed",
                Detail = "node_id header/body mismatch",
                Status = StatusCodes.Status400BadRequest
            });
        }

        var result = await _heartbeat.ProcessHeartbeatAsync(request, cancellationToken)
            .ConfigureAwait(false);
        return Ok(result);
    }

    /// <summary>GET /api/v1/node/config — authenticated node config pull.</summary>
    [HttpGet("node/config")]
    [NodeAuth]
    [RateLimit]
    public async Task<ActionResult<NodeConfigResponse>> GetConfig(CancellationToken cancellationToken)
    {
        var nodeId = AuthTokenExtractor.GetNodeId(HttpContext)
                     ?? throw new InvalidOperationException("node id missing after NodeAuth");
        var result = await _registration.GetConfigAsync(nodeId, cancellationToken)
            .ConfigureAwait(false);
        return Ok(result);
    }

    /// <summary>
    /// GET /api/v1/node/ticket-keys — Access Ticket verification public keys
    /// (never private keys); compatible with Go ticketkeys.File.
    /// </summary>
    [HttpGet("node/ticket-keys")]
    [NodeAuth]
    [RateLimit]
    public async Task<ActionResult<NodeTicketKeysResponse>> GetTicketKeys(CancellationToken cancellationToken)
    {
        _ = AuthTokenExtractor.GetNodeId(HttpContext)
            ?? throw new InvalidOperationException("node id missing after NodeAuth");

        var verificationKeys = await _signingKeys.GetVerificationKeysAsync(cancellationToken)
            .ConfigureAwait(false);

        var keys = new Dictionary<string, string>(verificationKeys.Count);
        foreach (var k in verificationKeys)
            keys[k.KeyId] = Convert.ToBase64String(k.PublicKey);

        return Ok(new NodeTicketKeysResponse
        {
            Issuer = _signing.Issuer,
            Keys = keys,
            UpdatedAt = DateTimeOffset.UtcNow.ToUnixTimeSeconds()
        });
    }
}
