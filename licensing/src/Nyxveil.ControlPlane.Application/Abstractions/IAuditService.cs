using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IAuditService
{
    Task WriteAsync(AuditWriteRequest request, CancellationToken cancellationToken = default);
}
