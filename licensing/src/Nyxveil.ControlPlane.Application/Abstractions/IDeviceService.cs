using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IDeviceService
{
    Task<DeviceActivateResponse> ActivateAsync(DeviceActivateRequest request, CancellationToken cancellationToken = default);

    Task RemoveAsync(string licenseToken, string deviceId, CancellationToken cancellationToken = default);

    Task RevokeAsync(string deviceId, CancellationToken cancellationToken = default);

    Task DisableAsync(string deviceId, CancellationToken cancellationToken = default);

    Task<IReadOnlyList<DeviceListItemDto>> ListByLicenseAsync(Guid licenseId, CancellationToken cancellationToken = default);
}
