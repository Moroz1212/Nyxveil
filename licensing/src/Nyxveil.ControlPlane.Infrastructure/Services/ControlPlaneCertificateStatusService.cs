using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class ControlPlaneCertificateStatusService : IControlPlaneCertificateStatusService
{
    private readonly IConfiguration? _configuration;
    private readonly IClock _clock;
    private readonly CertificateExpiryOptions _expiry;

    public ControlPlaneCertificateStatusService(
        IClock clock,
        IConfiguration? configuration = null,
        IOptions<CertificateExpiryOptions>? expiry = null)
    {
        _clock = clock;
        _configuration = configuration;
        _expiry = expiry?.Value ?? new CertificateExpiryOptions();
    }

    public Task<ControlPlaneCertificateStatusDto> GetStatusAsync(CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var now = _clock.UtcNow;
        var dto = new ControlPlaneCertificateStatusDto
        {
            Hostname = _configuration?["Hosting:PublicHostname"] ?? Environment.MachineName,
            Store = _configuration?["Certificate:StoreName"] ?? "My"
        };

        try
        {
            var installDir = _configuration?["Hosting:InstallDir"]
                             ?? AppContext.BaseDirectory.TrimEnd(
                                 Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
            if (string.IsNullOrWhiteSpace(installDir) || !Directory.Exists(installDir))
                return Task.FromResult(dto);

            var report = TlsConfigureEngine.CreateDefault().Status(installDir);
            dto.Hostname = string.IsNullOrWhiteSpace(report.PublicHostname) ? dto.Hostname : report.PublicHostname;
            dto.Subject = report.Subject;
            dto.Issuer = report.Issuer;
            dto.San = report.San;
            dto.NotBefore = report.NotBefore;
            dto.NotAfter = report.NotAfter;
            dto.Thumbprint = report.Thumbprint;
            dto.HasPrivateKey = report.HasPrivateKey;
            dto.Store = string.IsNullOrWhiteSpace(report.CertificateMode) ? dto.Store : report.CertificateMode;
            dto.DaysRemaining = CertificateExpiry.DaysRemaining(report.NotAfter?.UtcDateTime, now);
            dto.Health = CertificateExpiry.Evaluate(report.NotAfter?.UtcDateTime, now, _expiry).ToString();
            dto.SystemTrustOk = report.PublicTrustResult.Contains("ok", StringComparison.OrdinalIgnoreCase)
                                || string.Equals(report.PublicTrustResult, "trusted", StringComparison.OrdinalIgnoreCase)
                                || report.PublicTrustResult.StartsWith("OK", StringComparison.OrdinalIgnoreCase);
            // Prefer explicit trust probe when thumbprint loaded via CertificateLoader.
            if (!string.IsNullOrWhiteSpace(report.Thumbprint) && !string.IsNullOrWhiteSpace(dto.Hostname))
            {
                var opts = CertificateLoader.PreferredStoreConfig(report.Thumbprint);
                if (CertificateLoader.TryLoad(opts, dto.Hostname, out var cert, out _) && cert is not null)
                {
                    using (cert)
                    {
                        var trust = TlsCertificateGate.EvaluateSystemTrust(cert, dto.Hostname);
                        dto.SystemTrustOk = trust.Ok;
                    }
                }
            }

            dto.ServicePrivateKeyAccessOk =
                report.ServiceAccountPrivateKeyAccess.Contains("OK", StringComparison.OrdinalIgnoreCase);
        }
        catch
        {
            dto.Health = "Unknown";
        }

        return Task.FromResult(dto);
    }
}
