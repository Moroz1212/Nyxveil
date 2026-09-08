namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface IServerReleaseService
{
    Task<ServerReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default);

    Task<ServerReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default);
}

public sealed class ServerReleaseInfo
{
    public string? LatestVersion { get; set; }
    public string? ReleaseTag { get; set; }
    public string? ReleaseUrl { get; set; }
    public DateTimeOffset? PublishedAt { get; set; }
    public DateTimeOffset? LastCheckedAt { get; set; }
    public string SourceStatus { get; set; } = "unknown";
    public string? ErrorMessage { get; set; }
}
