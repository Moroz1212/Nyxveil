using System.Net.Http.Headers;
using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Application.SelfUpdate;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class ControlPlaneReleaseService : IControlPlaneReleaseService
{
    private readonly IHttpClientFactory _httpClientFactory;
    private readonly ServerReleasePolicyOptions _options;
    private readonly IClock _clock;
    private readonly ILogger<ControlPlaneReleaseService> _logger;
    private readonly object _gate = new();
    private ControlPlaneReleaseInfo _cache = new() { SourceStatus = "never_checked" };
    private string? _etag;

    public ControlPlaneReleaseService(
        IHttpClientFactory httpClientFactory,
        IOptions<ServerReleasePolicyOptions> options,
        IClock clock,
        ILogger<ControlPlaneReleaseService> logger)
    {
        _httpClientFactory = httpClientFactory;
        _options = options.Value;
        _clock = clock;
        _logger = logger;
    }

    public Task<ControlPlaneReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default)
    {
        lock (_gate)
        {
            var age = _cache.LastCheckedAt is null
                ? TimeSpan.MaxValue
                : _clock.UtcNow - _cache.LastCheckedAt.Value.UtcDateTime;
            if (age < TimeSpan.FromMinutes(Math.Max(1, _options.CacheMinutes)) &&
                _cache.SourceStatus is "ok" or "cached")
                return Task.FromResult(Clone(_cache));
        }

        return RefreshAsync(cancellationToken);
    }

    public async Task<ControlPlaneReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            var client = _httpClientFactory.CreateClient("GitHubReleases");
            var owner = string.IsNullOrWhiteSpace(_options.GitHubOwner) ? "Moroz1212" : _options.GitHubOwner;
            var repo = string.IsNullOrWhiteSpace(_options.GitHubRepo) ? "Nyxveil" : _options.GitHubRepo;
            var url = $"https://api.github.com/repos/{owner}/{repo}/releases?per_page=100";
            using var req = new HttpRequestMessage(HttpMethod.Get, url);
            req.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/vnd.github+json"));
            string? etag;
            lock (_gate) etag = _etag;
            if (!string.IsNullOrEmpty(etag))
                req.Headers.TryAddWithoutValidation("If-None-Match", etag);

            using var resp = await client.SendAsync(req, cancellationToken).ConfigureAwait(false);
            if (resp.StatusCode == System.Net.HttpStatusCode.NotModified)
            {
                lock (_gate)
                {
                    _cache.LastCheckedAt = _clock.UtcNow;
                    _cache.SourceStatus = "cached";
                    return Clone(_cache);
                }
            }

            resp.EnsureSuccessStatusCode();
            await using var stream = await resp.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
            var releases = await JsonSerializer.DeserializeAsync<List<GhRelease>>(stream, cancellationToken: cancellationToken)
                .ConfigureAwait(false) ?? new List<GhRelease>();

            var best = PickLatest(releases, allowPrerelease: false);
            var info = best ?? new ControlPlaneReleaseInfo
            {
                SourceStatus = "empty",
                LastCheckedAt = _clock.UtcNow
            };
            if (best is not null)
            {
                info.SourceStatus = "ok";
                info.LastCheckedAt = _clock.UtcNow;
                // Prefetch checksum sidecar so UI can show package verification before download.
                try
                {
                    if (!string.IsNullOrWhiteSpace(info.ChecksumUrl))
                    {
                        var sumText = await client.GetStringAsync(info.ChecksumUrl, cancellationToken)
                            .ConfigureAwait(false);
                        info.ExpectedSha256 = ControlPlaneReleasePolicy.ParseSha256Sidecar(sumText);
                    }
                }
                catch (Exception ex)
                {
                    _logger.LogWarning(ex, "Failed to prefetch Control Plane release checksum");
                    info.ExpectedSha256 = null;
                }
            }

            lock (_gate)
            {
                _cache = info;
                _etag = resp.Headers.ETag?.ToString();
            }
            return Clone(info);
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
        catch (Exception ex)
        {
            _logger.LogWarning(ex, "GitHub control-plane release refresh failed");
            lock (_gate)
            {
                _cache.LastCheckedAt = _clock.UtcNow;
                _cache.SourceStatus = string.IsNullOrWhiteSpace(_cache.LatestVersion) ? "error" : "stale";
                _cache.ErrorMessage = Truncate(ex.Message, 200);
                return Clone(_cache);
            }
        }
    }

    public static ControlPlaneReleaseInfo? PickLatest(IEnumerable<GhRelease> releases, bool allowPrerelease)
    {
        ControlPlaneReleaseInfo? best = null;
        SemVersion? bestVer = null;
        foreach (var r in releases)
        {
            if (!ControlPlaneReleasePolicy.IsAcceptableRelease(r.Draft, r.Prerelease, allowPrerelease))
                continue;
            if (!ControlPlaneReleasePolicy.TryParseTag(r.TagName, out var version))
                continue;
            if (!SemVersion.TryParse(version, out var sem))
                continue;

            var pkgName = ControlPlaneReleasePolicy.ExpectedPackageName(version);
            var sumName = ControlPlaneReleasePolicy.ExpectedChecksumName(version);
            var pkg = r.Assets?.FirstOrDefault(a =>
                string.Equals(a.Name, pkgName, StringComparison.OrdinalIgnoreCase));
            var sum = r.Assets?.FirstOrDefault(a =>
                string.Equals(a.Name, sumName, StringComparison.OrdinalIgnoreCase));
            if (pkg is null || sum is null || string.IsNullOrWhiteSpace(pkg.BrowserDownloadUrl) ||
                string.IsNullOrWhiteSpace(sum.BrowserDownloadUrl))
                continue;

            if (bestVer is null || sem.CompareTo(bestVer.Value) > 0)
            {
                bestVer = sem;
                best = new ControlPlaneReleaseInfo
                {
                    LatestVersion = version,
                    ReleaseTag = r.TagName,
                    ReleaseUrl = r.HtmlUrl,
                    PublishedAt = r.PublishedAt,
                    PackageName = pkg.Name,
                    PackageUrl = pkg.BrowserDownloadUrl,
                    ChecksumName = sum.Name,
                    ChecksumUrl = sum.BrowserDownloadUrl
                };
            }
        }

        return best;
    }

    private static ControlPlaneReleaseInfo Clone(ControlPlaneReleaseInfo s) => new()
    {
        LatestVersion = s.LatestVersion,
        ReleaseTag = s.ReleaseTag,
        ReleaseUrl = s.ReleaseUrl,
        PublishedAt = s.PublishedAt,
        PackageName = s.PackageName,
        PackageUrl = s.PackageUrl,
        ChecksumName = s.ChecksumName,
        ChecksumUrl = s.ChecksumUrl,
        ExpectedSha256 = s.ExpectedSha256,
        SourceStatus = s.SourceStatus,
        ErrorMessage = s.ErrorMessage,
        LastCheckedAt = s.LastCheckedAt
    };

    private static string Truncate(string s, int max) => s.Length <= max ? s : s[..max];

    public sealed class GhRelease
    {
        [JsonPropertyName("tag_name")] public string? TagName { get; set; }
        [JsonPropertyName("html_url")] public string? HtmlUrl { get; set; }
        [JsonPropertyName("draft")] public bool Draft { get; set; }
        [JsonPropertyName("prerelease")] public bool Prerelease { get; set; }
        [JsonPropertyName("published_at")] public DateTimeOffset? PublishedAt { get; set; }
        [JsonPropertyName("assets")] public List<GhAsset>? Assets { get; set; }
    }

    public sealed class GhAsset
    {
        [JsonPropertyName("name")] public string? Name { get; set; }
        [JsonPropertyName("browser_download_url")] public string? BrowserDownloadUrl { get; set; }
    }
}
