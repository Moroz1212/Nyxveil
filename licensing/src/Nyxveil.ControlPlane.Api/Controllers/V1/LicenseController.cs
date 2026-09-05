using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class LicenseController : ControllerBase
{
    private readonly ILicenseProvisioningService _licenses;

    public LicenseController(ILicenseProvisioningService licenses)
    {
        _licenses = licenses;
    }

    /// <summary>POST /api/v1/license/validate — anonymous; token in body.</summary>
    [HttpPost("license/validate")]
    [RateLimit]
    public async Task<ActionResult<LicenseValidateResponse>> Validate(
        [FromBody] LicenseValidateRequest request,
        CancellationToken cancellationToken)
    {
        var result = await _licenses.ValidateLicenseTokenAsync(request, cancellationToken)
            .ConfigureAwait(false);
        return Ok(result);
    }
}
