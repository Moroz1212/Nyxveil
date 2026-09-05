using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
[LicenseAuth]
public sealed class CatalogController : ControllerBase
{
    private readonly ICatalogService _catalog;

    public CatalogController(ICatalogService catalog)
    {
        _catalog = catalog;
    }

    /// <summary>GET /api/v1/catalog — requires license Bearer or access ticket.</summary>
    [HttpGet("catalog")]
    [RateLimit]
    public async Task<ActionResult<SignedCatalogDto>> GetCatalog(CancellationToken cancellationToken)
    {
        var signed = await LoadAsync(cancellationToken).ConfigureAwait(false);
        return Ok(signed);
    }

    /// <summary>GET /api/v1/locations — filtered location list.</summary>
    [HttpGet("locations")]
    [RateLimit]
    public async Task<ActionResult<IReadOnlyList<LocationDto>>> GetLocations(CancellationToken cancellationToken)
    {
        var signed = await LoadAsync(cancellationToken).ConfigureAwait(false);
        return Ok(signed.Catalog.Locations);
    }

    /// <summary>GET /api/v1/nodes — filtered node registry view.</summary>
    [HttpGet("nodes")]
    [RateLimit]
    public async Task<ActionResult<IReadOnlyList<NodeRegistryEntryDto>>> GetNodes(CancellationToken cancellationToken)
    {
        var signed = await LoadAsync(cancellationToken).ConfigureAwait(false);
        return Ok(signed.Catalog.Nodes);
    }

    private Task<SignedCatalogDto> LoadAsync(CancellationToken cancellationToken) =>
        _catalog.GetSignedCatalogForCallerAsync(
            AuthTokenExtractor.GetTicketClaims(HttpContext),
            AuthTokenExtractor.GetLicenseToken(HttpContext),
            cancellationToken);
}
