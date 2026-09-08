namespace Nyxveil.ControlPlane.Application.Options;

public sealed class ServerReleasePolicyOptions
{
    public const string SectionName = "ServerReleasePolicy";

    /// <summary>When null, Unsupported status is never assigned.</summary>
    public string? MinimumSupportedVersion { get; set; }

    public int CacheMinutes { get; set; } = 10;

    public string GitHubOwner { get; set; } = "Moroz1212";

    public string GitHubRepo { get; set; } = "Nyxveil";

    public bool AllowPrerelease { get; set; }
}

public sealed class SigningKeyRotationOptions
{
    public const string SectionName = "SigningKeyRotation";

    public TimeSpan RetiringGracePeriod { get; set; } = TimeSpan.FromHours(2);

    public TimeSpan NextPrepublishPeriod { get; set; } = TimeSpan.FromMinutes(15);

    public TimeSpan ClockSkewMargin { get; set; } = TimeSpan.FromMinutes(5);

    public TimeSpan PropagationMargin { get; set; } = TimeSpan.FromMinutes(5);

    /// <summary>Catalog lifetime in Control Plane is 1 hour (CatalogService).</summary>
    public TimeSpan CatalogLifetime { get; set; } = TimeSpan.FromHours(1);

    public TimeSpan ComputeMinimumGrace(TimeSpan ticketTtl) =>
        Max(CatalogLifetime, ticketTtl) + ClockSkewMargin + PropagationMargin;

    public void EnsureGraceSafe(TimeSpan ticketTtl)
    {
        var min = ComputeMinimumGrace(ticketTtl);
        if (RetiringGracePeriod < min)
        {
            throw new InvalidOperationException(
                $"SigningKeyRotation:RetiringGracePeriod ({RetiringGracePeriod}) is below safe minimum ({min}).");
        }
    }

    private static TimeSpan Max(TimeSpan a, TimeSpan b) => a >= b ? a : b;
}
