using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Canonical catalog JSON + Ed25519 signature matching Go controlplane/catalog.
/// </summary>
public sealed class CatalogSigner : ICatalogSigner
{
    private readonly ISigningKeyService _keys;
    private readonly IClock _clock;

    public CatalogSigner(ISigningKeyService keys, IClock clock)
    {
        _keys = keys;
        _clock = clock;
    }

    public async Task<SignedCatalogDto> SignAsync(CatalogDto catalog, CancellationToken cancellationToken = default)
    {
        var material = await _keys.GetCurrentSigningMaterialAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        catalog.IssuedAt = DateTime.SpecifyKind(now, DateTimeKind.Utc);
        if (catalog.ExpiresAt == default)
            catalog.ExpiresAt = catalog.IssuedAt.AddHours(1);
        else
            catalog.ExpiresAt = DateTime.SpecifyKind(catalog.ExpiresAt.ToUniversalTime(), DateTimeKind.Utc);

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(catalog);
        var signature = Ed25519SigningKeyStore.Sign(material.PrivateKey, payload);

        return new SignedCatalogDto
        {
            Catalog = catalog,
            KeyId = material.KeyId,
            Signature = signature
        };
    }
}
