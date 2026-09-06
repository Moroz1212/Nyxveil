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
[LicenseAuth]
public sealed class CatalogController : ControllerBase
{
    private readonly ICatalogService _catalog;
    private readonly ISigningKeyService _signingKeys;
    private readonly SigningOptions _signing;

    public CatalogController(
        ICatalogService catalog,
        ISigningKeyService signingKeys,
        IOptions<SigningOptions> signing)
    {
        _catalog = catalog;
        _signingKeys = signingKeys;
        _signing = signing.Value;
    }

    /// <summary>GET /api/v1/catalog — requires license Bearer or access ticket.</summary>
    [HttpGet("catalog")]
    [RateLimit]
    public async Task<ActionResult<SignedCatalogDto>> GetCatalog(CancellationToken cancellationToken)
    {
        var signed = await LoadAsync(cancellationToken).ConfigureAwait(false);
        return Ok(signed);
    }

    /// <summary>
    /// GET /api/v1/catalog-keys — catalog signing verification public keys only
    /// (current + next). Same ring as CatalogSigner. Never returns private keys.
    /// </summary>
    [HttpGet("catalog-keys")]
    [RateLimit]
    public async Task<ActionResult<CatalogKeysResponse>> GetCatalogKeys(CancellationToken cancellationToken)
    {
        var verificationKeys = await _signingKeys.GetVerificationKeysAsync(cancellationToken)
            .ConfigureAwait(false);

        var keys = new Dictionary<string, string>(verificationKeys.Count);
        foreach (var k in verificationKeys)
            keys[k.KeyId] = Convert.ToBase64String(k.PublicKey);

        return Ok(new CatalogKeysResponse
        {
            Issuer = _signing.Issuer,
            Keys = keys,
            UpdatedAt = DateTimeOffset.UtcNow.ToUnixTimeSeconds()
        });
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
