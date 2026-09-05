using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface ILicenseProvisioningService
{
    Task<CreateLicenseResponse> CreateLicenseAsync(CreateLicenseRequest request, CancellationToken cancellationToken = default);

    Task ExtendLicenseAsync(ExtendLicenseRequest request, CancellationToken cancellationToken = default);

    Task DisableLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default);

    Task EnableLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default);

    Task RevokeLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default);

    Task<LicenseValidateResponse> ValidateLicenseTokenAsync(LicenseValidateRequest request, CancellationToken cancellationToken = default);
}
