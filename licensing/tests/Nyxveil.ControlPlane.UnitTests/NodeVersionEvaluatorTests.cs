using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeVersionEvaluatorTests
{
    [Theory]
    [InlineData("1.1.9", "1.1.9", null, NodeVersionStatus.Current)]
    [InlineData("1.1.8", "1.1.9", null, NodeVersionStatus.UpdateAvailable)]
    [InlineData("1.2.0", "1.1.9", null, NodeVersionStatus.Ahead)]
    [InlineData(null, "1.1.9", null, NodeVersionStatus.Unknown)]
    [InlineData("not-a-version", "1.1.9", null, NodeVersionStatus.Invalid)]
    [InlineData("1.1.6", "1.1.9", "1.1.7", NodeVersionStatus.Unsupported)]
    [InlineData("1.1.6", "1.1.9", null, NodeVersionStatus.UpdateAvailable)]
    [InlineData("v1.1.9", "1.1.9", null, NodeVersionStatus.Current)]
    public void Evaluate_Matrix(string? installed, string? latest, string? min, NodeVersionStatus expected)
    {
        Assert.Equal(expected, NodeVersionEvaluator.Evaluate(installed, latest, min));
    }

    [Fact]
    public void PickLatest_FiltersControlPlaneAndPrerelease()
    {
        var releases = new[]
        {
            new ServerReleaseService.GhRelease { TagName = "controlplane-v1.3.0", Draft = false, Prerelease = false },
            new ServerReleaseService.GhRelease { TagName = "server-v1.1.8", Draft = false, Prerelease = false },
            new ServerReleaseService.GhRelease { TagName = "server-v1.1.9", Draft = false, Prerelease = false },
            new ServerReleaseService.GhRelease { TagName = "server-v1.2.0-rc.1", Draft = false, Prerelease = false },
            new ServerReleaseService.GhRelease { TagName = "server-v1.2.0", Draft = true, Prerelease = false },
        };

        var best = ServerReleaseService.PickLatestStableServer(releases, allowPrerelease: false);
        Assert.NotNull(best);
        Assert.Equal("1.1.9", best!.Value.Version);
        Assert.Equal("server-v1.1.9", best.Value.Tag);
    }
}
