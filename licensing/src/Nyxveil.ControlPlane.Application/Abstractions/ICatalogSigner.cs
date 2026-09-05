using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// Signs catalogs using Go Core canonical JSON + Ed25519 (core/controlplane/catalog).
/// </summary>
public interface ICatalogSigner
{
    Task<SignedCatalogDto> SignAsync(CatalogDto catalog, CancellationToken cancellationToken = default);
}
