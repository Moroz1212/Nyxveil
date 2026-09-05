using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface ICatalogService
{
    /// <summary>
    /// Builds and signs a catalog filtered for the authenticated caller (license or ticket).
    /// </summary>
    Task<SignedCatalogDto> GetSignedCatalogForCallerAsync(
        AccessTicketClaims? ticketClaims,
        string? licenseToken,
        CancellationToken cancellationToken = default);
}
