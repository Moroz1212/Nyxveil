using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IRevocationService
{
    Task<RevocationListResponse> GetSnapshotForNodeAsync(string nodeId, CancellationToken cancellationToken = default);

    Task RevokeTicketAsync(string jti, CancellationToken cancellationToken = default);

    Task RevokeLicenseAsync(string licenseId, CancellationToken cancellationToken = default);

    Task RevokeDeviceAsync(string deviceId, CancellationToken cancellationToken = default);
}
