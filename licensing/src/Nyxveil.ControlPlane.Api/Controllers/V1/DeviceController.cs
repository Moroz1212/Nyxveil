using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.Contracts.V1;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class DeviceController : ControllerBase
{
    private readonly IDeviceService _devices;

    public DeviceController(IDeviceService devices)
    {
        _devices = devices;
    }

    /// <summary>POST /api/v1/device/activate — license token in body.</summary>
    [HttpPost("device/activate")]
    [RateLimit]
    public async Task<ActionResult<DeviceActivateResponse>> Activate(
        [FromBody] DeviceActivateRequest request,
        CancellationToken cancellationToken)
    {
        var result = await _devices.ActivateAsync(request, cancellationToken).ConfigureAwait(false);
        return Ok(result);
    }

    /// <summary>POST /api/v1/device/remove — license token in body.</summary>
    [HttpPost("device/remove")]
    [RateLimit]
    public async Task<ActionResult<DeviceRemoveResponse>> Remove(
        [FromBody] DeviceRemoveRequest request,
        CancellationToken cancellationToken)
    {
        await _devices.RemoveAsync(request.LicenseToken, request.DeviceId, cancellationToken)
            .ConfigureAwait(false);
        return Ok(new DeviceRemoveResponse { Removed = true });
    }
}
