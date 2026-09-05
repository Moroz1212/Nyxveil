using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class VersionController : ControllerBase
{
    /// <summary>GET /api/v1/version — public protocol/version info.</summary>
    [HttpGet("version")]
    public ActionResult<VersionResponse> GetVersion()
    {
        return Ok(new VersionResponse());
    }
}
