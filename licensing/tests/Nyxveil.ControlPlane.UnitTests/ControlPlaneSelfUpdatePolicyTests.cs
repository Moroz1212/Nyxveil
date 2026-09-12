using System.IO.Compression;
using System.Text;
using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class ControlPlaneSelfUpdatePolicyTests
{
    [Theory]
    [InlineData("1.3.4", "1.3.5", ControlPlaneUpdateAvailability.UpdateAvailable)]
    [InlineData("1.3.5", "1.3.5", ControlPlaneUpdateAvailability.Current)]
    [InlineData("1.3.6", "1.3.5", ControlPlaneUpdateAvailability.InstalledNewer)]
    [InlineData("bad", "1.3.5", ControlPlaneUpdateAvailability.Unknown)]
    public void Compare_Versions(string installed, string latest, ControlPlaneUpdateAvailability expected)
    {
        Assert.Equal(expected, ControlPlaneReleasePolicy.Compare(installed, latest));
    }

    [Fact]
    public void PrereleaseAndDraft_IgnoredForStable()
    {
        Assert.False(ControlPlaneReleasePolicy.IsAcceptableRelease(draft: true, prerelease: false, allowPrerelease: false));
        Assert.False(ControlPlaneReleasePolicy.IsAcceptableRelease(draft: false, prerelease: true, allowPrerelease: false));
        Assert.True(ControlPlaneReleasePolicy.IsAcceptableRelease(draft: false, prerelease: false, allowPrerelease: false));
    }

    [Theory]
    [InlineData("control-plane-v1.3.5", true, "1.3.5")]
    [InlineData("server-v1.1.14", false, "")]
    [InlineData("control-plane-v", false, "")]
    [InlineData("control-plane-v1.3", false, "")]
    public void TryParseTag(string tag, bool ok, string version)
    {
        var parsed = ControlPlaneReleasePolicy.TryParseTag(tag, out var v);
        Assert.Equal(ok, parsed);
        if (ok) Assert.Equal(version, v);
    }

    [Fact]
    public void MissingPackageOrChecksum_RejectedByPicker()
    {
        var releases = new List<ControlPlaneReleaseService.GhRelease>
        {
            new()
            {
                TagName = "control-plane-v1.3.5",
                Draft = false,
                Prerelease = false,
                Assets = new List<ControlPlaneReleaseService.GhAsset>
                {
                    new() { Name = "Nyxveil-ControlPlane-v1.3.5-release.zip", BrowserDownloadUrl = "https://example/p.zip" }
                    // checksum missing
                }
            }
        };
        Assert.Null(ControlPlaneReleaseService.PickLatest(releases, allowPrerelease: false));
    }

    [Fact]
    public void PickLatest_SelectsHighestValid()
    {
        var releases = new List<ControlPlaneReleaseService.GhRelease>
        {
            Make("control-plane-v1.3.4"),
            Make("control-plane-v1.3.5"),
            Make("control-plane-v1.3.5-rc.1", prerelease: true)
        };
        var best = ControlPlaneReleaseService.PickLatest(releases, allowPrerelease: false);
        Assert.NotNull(best);
        Assert.Equal("1.3.5", best!.LatestVersion);
    }

    [Fact]
    public void ParseSha256Sidecar()
    {
        var hex = new string('a', 64);
        Assert.Equal(hex.ToUpperInvariant(), ControlPlaneReleasePolicy.ParseSha256Sidecar($"{hex}  file.zip\n"));
        Assert.Null(ControlPlaneReleasePolicy.ParseSha256Sidecar("not-a-hash"));
    }

    [Fact]
    public void IncorrectSha_Detected()
    {
        var bytes = Encoding.UTF8.GetBytes("hello");
        var actual = ControlPlaneReleasePolicy.Sha256Hex(bytes);
        var expected = ControlPlaneReleasePolicy.Sha256Hex(Encoding.UTF8.GetBytes("other"));
        Assert.NotEqual(actual, expected);
    }

    [Fact]
    public void ZipSlip_Rejected()
    {
        var tmp = Path.Combine(Path.GetTempPath(), "cp-zip-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(tmp);
        var zip = Path.Combine(tmp, "evil.zip");
        var dest = Path.Combine(tmp, "out");
        Directory.CreateDirectory(dest);
        using (var archive = ZipFile.Open(zip, ZipArchiveMode.Create))
        {
            var entry = archive.CreateEntry("../escape.txt");
            using var s = entry.Open();
            s.Write(Encoding.UTF8.GetBytes("x"));
        }

        Assert.Throws<InvalidOperationException>(() =>
            ControlPlaneReleasePolicy.AssertSafeZipEntries(zip, dest));
    }

    [Fact]
    public void PackageVersionMatches()
    {
        var root = Path.Combine(Path.GetTempPath(), "cp-ver-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        File.WriteAllText(Path.Combine(root, "VERSION"), "1.3.5\n");
        Assert.True(ControlPlaneReleasePolicy.PackageVersionMatches(root, "1.3.5"));
        Assert.False(ControlPlaneReleasePolicy.PackageVersionMatches(root, "1.3.4"));
    }

    [Fact]
    public void ExpectedAssetNames()
    {
        Assert.Equal("Nyxveil-ControlPlane-v1.3.5-release.zip",
            ControlPlaneReleasePolicy.ExpectedPackageName("1.3.5"));
        Assert.Equal("Nyxveil-ControlPlane-v1.3.5-release.zip.sha256",
            ControlPlaneReleasePolicy.ExpectedChecksumName("1.3.5"));
    }

    private static ControlPlaneReleaseService.GhRelease Make(string tag, bool prerelease = false)
    {
        Assert.True(ControlPlaneReleasePolicy.TryParseTag(tag, out var version));
        return new ControlPlaneReleaseService.GhRelease
        {
            TagName = tag,
            Draft = false,
            Prerelease = prerelease,
            HtmlUrl = "https://example/" + tag,
            Assets = new List<ControlPlaneReleaseService.GhAsset>
            {
                new()
                {
                    Name = ControlPlaneReleasePolicy.ExpectedPackageName(version),
                    BrowserDownloadUrl = "https://example/p.zip"
                },
                new()
                {
                    Name = ControlPlaneReleasePolicy.ExpectedChecksumName(version),
                    BrowserDownloadUrl = "https://example/p.sha256"
                }
            }
        };
    }
}
