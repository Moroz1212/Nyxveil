using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class RevocationController : ControllerBase
{
    private readonly IRevocationService _revocation;

    public RevocationController(IRevocationService revocation)
    {
        _revocation = revocation;
    }

    /// <summary>
    /// GET /api/v1/revocation — NODE ONLY (X-Node-Id + req-v2 signature).
    /// User license tokens are rejected with 403 by <see cref="NodeAuthAttribute"/>.
    /// </summary>
    [HttpGet("revocation")]
    [NodeAuth]
    [RateLimit]
    public async Task<ActionResult<RevocationListResponse>> GetRevocation(CancellationToken cancellationToken)
    {
        var nodeId = AuthTokenExtractor.GetNodeId(HttpContext)
                     ?? throw new InvalidOperationException("node id missing after NodeAuth");
        var result = await _revocation.GetSnapshotForNodeAsync(nodeId, cancellationToken)
            .ConfigureAwait(false);
        return Ok(result);
    }
}
