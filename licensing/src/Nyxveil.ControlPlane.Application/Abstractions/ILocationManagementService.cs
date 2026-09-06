namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface ILocationManagementService
{
    Task DeleteLocationAsync(string locationId, string actor, CancellationToken cancellationToken = default);
}
