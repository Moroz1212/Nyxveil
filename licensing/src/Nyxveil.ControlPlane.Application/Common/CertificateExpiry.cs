namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>Centralized certificate expiry thresholds (days remaining).</summary>
public sealed class CertificateExpiryOptions
{
    public const string SectionName = "CertificateExpiry";

    /// <summary>Above this → Healthy.</summary>
    public int HealthyDays { get; set; } = 30;

    /// <summary>At or below this (and above Critical) → Warning / ExpiringSoon.</summary>
    public int WarningDays { get; set; } = 30;

    /// <summary>At or below this (and above 0) → Critical.</summary>
    public int CriticalDays { get; set; } = 14;
}

public enum CertificateHealthStatus
{
    Unknown = 0,
    Healthy = 1,
    ExpiringSoon = 2,
    Critical = 3,
    Expired = 4,
    Invalid = 5
}

public static class CertificateExpiry
{
    public static CertificateHealthStatus Evaluate(DateTime? notAfterUtc, DateTime utcNow, CertificateExpiryOptions? options = null)
    {
        options ??= new CertificateExpiryOptions();
        if (notAfterUtc is null)
            return CertificateHealthStatus.Unknown;

        var days = (notAfterUtc.Value - utcNow).TotalDays;
        if (double.IsNaN(days) || double.IsInfinity(days))
            return CertificateHealthStatus.Invalid;
        if (days <= 0)
            return CertificateHealthStatus.Expired;
        if (days <= options.CriticalDays)
            return CertificateHealthStatus.Critical;
        if (days <= options.WarningDays)
            return CertificateHealthStatus.ExpiringSoon;
        return CertificateHealthStatus.Healthy;
    }

    public static int? DaysRemaining(DateTime? notAfterUtc, DateTime utcNow)
    {
        if (notAfterUtc is null)
            return null;
        return (int)Math.Floor((notAfterUtc.Value - utcNow).TotalDays);
    }
}
