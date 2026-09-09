using System.Net.Http.Headers;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class ServerReleaseService : IServerReleaseService
{
    private static readonly Regex ServerTag = new(
        @"^server-v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant | RegexOptions.Compiled);

    private readonly IHttpClientFactory _httpClientFactory;
    private readonly ServerReleasePolicyOptions _options;
    private readonly IClock _clock;
    private readonly ILogger<ServerReleaseService> _logger;
    private readonly object _gate = new();
    private ServerReleaseInfo _cache = new() { SourceStatus = "never_checked" };
    private string? _etag;

    public ServerReleaseService(
        IHttpClientFactory httpClientFactory,
        IOptions<ServerReleasePolicyOptions> options,
        IClock clock,
        ILogger<ServerReleaseService> logger)
    {
        _httpClientFactory = httpClientFactory;
        _options = options.Value;
        _clock = clock;
        _logger = logger;
    }

    public Task<ServerReleaseInfo> GetLatestAsync(CancellationToken cancellationToken = default)
    {
        lock (_gate)
        {
            var age = _cache.LastCheckedAt is null
                ? TimeSpan.MaxValue
                : _clock.UtcNow - _cache.LastCheckedAt.Value.UtcDateTime;
            if (age < TimeSpan.FromMinutes(Math.Max(1, _options.CacheMinutes)) &&
                _cache.SourceStatus is "ok" or "cached")
            {
                return Task.FromResult(Clone(_cache));
            }
        }

        return RefreshAsync(cancellationToken);
    }

    public async Task<ServerReleaseInfo> RefreshAsync(CancellationToken cancellationToken = default)
    {
        try
        {
            var client = _httpClientFactory.CreateClient("GitHubReleases");
            var url =
                "https://api.github.com/repos/Moroz1212/Nyxveil/releases?per_page=100";
            using var req = new HttpRequestMessage(HttpMethod.Get, url);
            req.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/vnd.github+json"));
            req.Headers.UserAgent.ParseAdd("Nyxveil-ControlPlane");
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

            var best = PickLatestStableServer(releases, _options.AllowPrerelease);
            var info = new ServerReleaseInfo
            {
                LastCheckedAt = _clock.UtcNow,
                SourceStatus = best is null ? "empty" : "ok"
            };
            if (best is not null)
            {
                info.LatestVersion = best.Value.Version;
                info.ReleaseTag = best.Value.Tag;
                info.ReleaseUrl = best.Value.HtmlUrl;
                info.PublishedAt = best.Value.PublishedAt;
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
            _logger.LogWarning(ex, "GitHub server release refresh failed");
            lock (_gate)
            {
                if (!string.IsNullOrWhiteSpace(_cache.LatestVersion))
                {
                    _cache.LastCheckedAt = _clock.UtcNow;
                    _cache.SourceStatus = "stale";
                    _cache.ErrorMessage = Truncate(ex.Message, 200);
                    return Clone(_cache);
                }

                var empty = new ServerReleaseInfo
                {
                    LastCheckedAt = _clock.UtcNow,
                    SourceStatus = "error",
                    ErrorMessage = Truncate(ex.Message, 200)
                };
                _cache = empty;
                return Clone(empty);
            }
        }
    }

    public static (string Version, string Tag, string? HtmlUrl, DateTimeOffset? PublishedAt)? PickLatestStableServer(
        IEnumerable<GhRelease> releases,
        bool allowPrerelease)
    {
        (string Version, string Tag, string? HtmlUrl, DateTimeOffset? PublishedAt)? best = null;
        SemVersion? bestVer = null;

        foreach (var r in releases)
        {
            if (r.Draft) continue;
            if (r.Prerelease && !allowPrerelease) continue;
            var tag = r.TagName?.Trim() ?? "";
            var m = ServerTag.Match(tag);
            if (!m.Success) continue;
            if (!SemVersion.TryParse(m.Groups[1].Value, out var ver)) continue;
            if (ver.IsPrerelease && !allowPrerelease) continue;

            if (bestVer is null || ver > bestVer.Value)
            {
                bestVer = ver;
                best = (ver.ToString(), tag, r.HtmlUrl, r.PublishedAt);
            }
        }

        return best;
    }

    private static ServerReleaseInfo Clone(ServerReleaseInfo s) => new()
    {
        LatestVersion = s.LatestVersion,
        ReleaseTag = s.ReleaseTag,
        ReleaseUrl = s.ReleaseUrl,
        PublishedAt = s.PublishedAt,
        LastCheckedAt = s.LastCheckedAt,
        SourceStatus = s.SourceStatus,
        ErrorMessage = s.ErrorMessage
    };

    private static string Truncate(string s, int n) => s.Length <= n ? s : s[..n];

    public sealed class GhRelease
    {
        [JsonPropertyName("tag_name")]
        public string? TagName { get; set; }

        [JsonPropertyName("draft")]
        public bool Draft { get; set; }

        [JsonPropertyName("prerelease")]
        public bool Prerelease { get; set; }

        [JsonPropertyName("html_url")]
        public string? HtmlUrl { get; set; }

        [JsonPropertyName("published_at")]
        public DateTimeOffset? PublishedAt { get; set; }
    }
}
