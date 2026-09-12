using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IAdminRealtimeNotifier
{
    Task NotifyDashboardAsync(CancellationToken cancellationToken = default);
    Task NotifyNodeAsync(string nodeId, CancellationToken cancellationToken = default);
    Task NotifyCommandAsync(Guid commandId, string nodeId, CancellationToken cancellationToken = default);
}

public interface IUpdatePreflightService
{
    Task<UpdatePreflightResult> EvaluateUpdateAsync(string nodeId, CancellationToken cancellationToken = default);
}

public interface ILocationRolloutService
{
    Task<LocationRolloutDto> StartAsync(string locationId, string actor, IEnumerable<string> roles, CancellationToken cancellationToken = default);
    Task<LocationRolloutDto?> GetAsync(Guid rolloutId, CancellationToken cancellationToken = default);
    Task<IReadOnlyList<LocationRolloutDto>> ListRecentAsync(int take = 20, CancellationToken cancellationToken = default);
    Task TickAsync(CancellationToken cancellationToken = default);
}

public sealed class LocationRolloutDto
{
    public Guid Id { get; init; }
    public string LocationId { get; init; } = "";
    public string? TargetVersion { get; init; }
    public string Status { get; init; } = "";
    public string CreatedBy { get; init; } = "";
    public DateTime CreatedAt { get; init; }
    public DateTime? CompletedAt { get; init; }
    public string? StopReason { get; init; }
    public IReadOnlyList<LocationRolloutItemDto> Items { get; init; } = Array.Empty<LocationRolloutItemDto>();
}

public sealed class LocationRolloutItemDto
{
    public string NodeId { get; init; } = "";
    public string DisplayName { get; init; } = "";
    public string Status { get; init; } = "";
    public Guid? CommandId { get; init; }
    public string? Detail { get; init; }
}
