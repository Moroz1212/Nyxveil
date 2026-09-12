using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IFleetOverviewService
{
    Task<FleetOverviewDto> GetOverviewAsync(FleetQuery? query = null, CancellationToken cancellationToken = default);
}
