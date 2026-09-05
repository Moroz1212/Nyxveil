using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.Contracts.V1;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class MasterController : ControllerBase
{
    private readonly ILicenseProvisioningService _licenses;

    public MasterController(ILicenseProvisioningService licenses)
    {
        _licenses = licenses;
    }

    /// <summary>POST /api/v1/master/access — returns whether master role is granted.</summary>
    [HttpPost("master/access")]
    [RateLimit]
    public async Task<ActionResult<MasterAccessResponse>> Access(
        [FromBody] MasterAccessRequest request,
        CancellationToken cancellationToken)
    {
        var result = await _licenses.ValidateLicenseTokenAsync(
                new LicenseValidateRequest { LicenseToken = request.LicenseToken },
                cancellationToken)
            .ConfigureAwait(false);

        // Master privilege is License.Role, never Plan code.
        var granted = result.Valid &&
                      string.Equals(result.Role, "master", StringComparison.OrdinalIgnoreCase);

        return Ok(new MasterAccessResponse
        {
            Role = granted ? "master" : (result.Role ?? "user"),
            Granted = granted
        });
    }
}
