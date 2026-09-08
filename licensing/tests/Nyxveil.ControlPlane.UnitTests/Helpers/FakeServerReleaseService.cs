using Nyxveil.ControlPlane.Application.Abstractions;

namespace Nyxveil.ControlPlane.UnitTests.Helpers;

public sealed class FakeServerReleaseService : IServerReleaseService
{
    public ServerReleaseInfo Info { get; set; } = new()
    {
        LatestVersion = "1.1.9",
        ReleaseTag = "server-v1.1.9",
        SourceStatus = "ok",
        LastCheckedAt = DateTimeOffset.UtcNow
    };

    public Task<ServerReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default) =>
        Task.FromResult(Info);

    public Task<ServerReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default) =>
        Task.FromResult(Info);
}
