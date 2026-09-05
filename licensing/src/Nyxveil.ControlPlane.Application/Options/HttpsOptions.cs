namespace Nyxveil.ControlPlane.Application.Options;

public sealed class HttpsOptions
{
    public const string SectionName = "Https";

    /// <summary>When true, reject non-HTTPS requests in Production environments.</summary>
    public bool RequireHttpsInProduction { get; set; } = true;
}
