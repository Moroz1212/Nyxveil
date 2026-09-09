using System.Diagnostics;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class ControlPlaneAcmeWizardService : IControlPlaneAcmeWizardService
{
    private readonly ControlPlaneDbContext _db;
    private readonly IClock _clock;
    private readonly IAcmeDns01Provider _acme;
    private readonly IConfiguration? _configuration;
    private readonly IDnsTxtLookup _dns;
    private readonly IControlPlaneCertificateStatusService _cpStatus;

    public ControlPlaneAcmeWizardService(
        ControlPlaneDbContext db,
        IClock clock,
        IAcmeDns01Provider acme,
        IDnsTxtLookup dns,
        IControlPlaneCertificateStatusService cpStatus,
        IConfiguration? configuration = null)
    {
        _db = db;
        _clock = clock;
        _acme = acme;
        _dns = dns;
        _cpStatus = cpStatus;
        _configuration = configuration;
    }

    public async Task<CertificateRenewalOperation> StartAsync(
        string actor,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(actor))
            throw new ValidationException("actor is required");

        await using var lease = await ManagementOperationLock.AcquireAsync(_db, "cp-certificate", cancellationToken);

        var domain = (_configuration?["Hosting:PublicHostname"] ?? string.Empty).Trim();
        if (string.IsNullOrWhiteSpace(domain))
            throw new ValidationException("Hosting:PublicHostname is required for ACME wizard");

        var active = await GetActiveAsync(cancellationToken).ConfigureAwait(false);
        if (active is not null)
            throw new ConflictException("an active certificate renewal operation already exists");

        var order = await _acme.CreateDns01OrderAsync(domain, cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var op = new CertificateRenewalOperation
        {
            Id = Guid.NewGuid(),
            Status = CertificateRenewalStatus.PendingDns,
            Domain = domain,
            CreatedAt = now,
            CreatedBy = actor.Trim(),
            UpdatedAt = now,
            AcmeOrderUrl = order.OrderUrl,
            ChallengeName = order.ChallengeName,
            ChallengeValue = order.ChallengeValue,
            ChallengeExpiresAt = order.ChallengeExpiresAt,
            OldThumbprint = _configuration?["Certificate:Thumbprint"]
        };

        _db.CertificateRenewalOperations.Add(op);
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await lease.CommitAsync(cancellationToken);
        return op;
    }

    public async Task<CertificateRenewalOperation?> GetActiveAsync(CancellationToken cancellationToken = default)
    {
        var terminal = new[]
        {
            CertificateRenewalStatus.Completed,
            CertificateRenewalStatus.Failed,
            CertificateRenewalStatus.Cancelled,
            CertificateRenewalStatus.Expired
        };
        var op = await _db.CertificateRenewalOperations
            .Where(o => !terminal.Contains(o.Status))
            .OrderByDescending(o => o.CreatedAt)
            .FirstOrDefaultAsync(cancellationToken)
            .ConfigureAwait(false);

        if (op is null)
            return null;

        if (op.ChallengeExpiresAt is not null &&
            op.ChallengeExpiresAt < _clock.UtcNow &&
            op.Status is CertificateRenewalStatus.PendingDns or CertificateRenewalStatus.DnsReady)
        {
            op.Status = CertificateRenewalStatus.Expired;
            op.ErrorMessage = "Запрос истёк. Создайте новый.";
            op.UpdatedAt = _clock.UtcNow;
            op.CompletedAt = op.UpdatedAt;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            return null;
        }

        if (op.Status == CertificateRenewalStatus.Switching)
            await TryCompleteAfterSwitchAsync(op, cancellationToken).ConfigureAwait(false);

        if (op.Status is CertificateRenewalStatus.Validating or CertificateRenewalStatus.Issuing
            && _clock.UtcNow - op.UpdatedAt > TimeSpan.FromMinutes(10))
        {
            op.Status = CertificateRenewalStatus.Failed;
            op.ErrorMessage = "Certificate operation interrupted or timed out; create a new request";
            op.CompletedAt = op.UpdatedAt = _clock.UtcNow;
            await _db.SaveChangesAsync(cancellationToken);
        }

        return op;
    }

    public async Task<CertificateRenewalOperation> GetStatusAsync(
        Guid id,
        CancellationToken cancellationToken = default)
    {
        var op = await _db.CertificateRenewalOperations
            .FirstOrDefaultAsync(o => o.Id == id, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("certificate renewal operation not found");

        if (op.Status == CertificateRenewalStatus.Switching)
            await TryCompleteAfterSwitchAsync(op, cancellationToken).ConfigureAwait(false);

        return op;
    }

    public async Task<DnsTxtVerifyResult> VerifyDnsTxtAsync(
        Guid id,
        CancellationToken cancellationToken = default)
    {
        var op = await LoadAsync(id, cancellationToken).ConfigureAwait(false);
        if (op.Status is not (CertificateRenewalStatus.PendingDns or CertificateRenewalStatus.DnsReady)
            || op.ChallengeExpiresAt <= _clock.UtcNow)
            throw new ConflictException("DNS challenge is not active or has expired");
        var host = string.IsNullOrWhiteSpace(op.ChallengeName)
            ? $"_acme-challenge.{op.Domain}"
            : op.ChallengeName;

        var records = await _dns.LookupTxtAsync(host, cancellationToken).ConfigureAwait(false);
        var match = records.FirstOrDefault(r =>
            string.Equals(r.Trim('"'), op.ChallengeValue, StringComparison.Ordinal));

        if (match is not null)
        {
            op.Status = CertificateRenewalStatus.DnsReady;
            op.UpdatedAt = _clock.UtcNow;
            op.ErrorMessage = null;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            return new DnsTxtVerifyResult
            {
                Found = true,
                ObservedValue = match.Trim('"'),
                Message = "TXT record matches challenge value"
            };
        }

        return new DnsTxtVerifyResult
        {
            Found = false,
            ObservedValue = records.FirstOrDefault(),
            Message = records.Count == 0
                ? "no TXT records found"
                : "TXT records present but challenge value not matched"
        };
    }

    public async Task<CertificateRenewalOperation> ContinueFinalizeAsync(
        Guid id,
        CancellationToken cancellationToken = default)
    {
        var op = await LoadAsync(id, cancellationToken).ConfigureAwait(false);
        if (op.Status != CertificateRenewalStatus.DnsReady || op.ChallengeExpiresAt <= _clock.UtcNow)
            throw new ConflictException("verify an unexpired DNS challenge before issuing");

        try
        {
            op.Status = CertificateRenewalStatus.Validating;
            op.UpdatedAt = _clock.UtcNow;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

            if (string.IsNullOrWhiteSpace(op.AcmeOrderUrl))
                throw new ValidationException("ACME order URL missing");

            await _acme.ValidateChallengeAsync(op.AcmeOrderUrl, cancellationToken).ConfigureAwait(false);

            op.Status = CertificateRenewalStatus.Issuing;
            op.UpdatedAt = _clock.UtcNow;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

            var issued = await _acme.FinalizeAsync(op.AcmeOrderUrl, cancellationToken).ConfigureAwait(false);
            op.NewThumbprint = issued.Thumbprint;
            op.Status = CertificateRenewalStatus.ReadyToImport;
            op.UpdatedAt = _clock.UtcNow;
            op.ErrorMessage = null;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            return op;
        }
        catch (Exception ex)
        {
            op.Status = CertificateRenewalStatus.Failed;
            op.ErrorMessage = Truncate(ex.Message, 1024);
            op.UpdatedAt = _clock.UtcNow;
            op.CompletedAt = op.UpdatedAt;
            using var persist = new CancellationTokenSource(TimeSpan.FromSeconds(10));
            await _db.SaveChangesAsync(persist.Token).ConfigureAwait(false);
            throw;
        }
    }

    public async Task<CertificateRenewalOperation> ImportAndSwitchAsync(
        Guid id,
        CancellationToken cancellationToken = default)
    {
        var op = await LoadAsync(id, cancellationToken).ConfigureAwait(false);
        if (op.Status != CertificateRenewalStatus.ReadyToImport)
            throw new ValidationException("operation is not ready to import");

        var (pfxPath, pfxPassword) = CertesAcmeDns01Provider.TryGetIssuedPfx(op.AcmeOrderUrl, op.NewThumbprint);
        if (string.IsNullOrWhiteSpace(pfxPath) || !File.Exists(pfxPath))
        {
            op.Status = CertificateRenewalStatus.Failed;
            op.UpdatedAt = _clock.UtcNow;
            op.CompletedAt = op.UpdatedAt;
            op.ErrorMessage = "Issued PFX is missing; certificate activation was not performed";
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            throw new ValidationException(op.ErrorMessage);
        }

        var hostname = op.Domain;
        var publicUrl = (_configuration?["Hosting:PublicBaseUrl"] ?? "").Trim();
        if (string.IsNullOrWhiteSpace(publicUrl))
            publicUrl = "https://" + hostname;

        var exe = Path.Combine(AppContext.BaseDirectory, "Nyxveil.ControlPlane.Web.exe");
        if (!OperatingSystem.IsWindows() || !File.Exists(exe))
            throw new ValidationException("certificate activation requires the installed Windows release executable");

        op.Status = CertificateRenewalStatus.Switching;
        op.UpdatedAt = _clock.UtcNow;
        op.ErrorMessage = null;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        // Detached local maintenance process reuses existing TlsConfigureEngine via CLI.
        var psi = new ProcessStartInfo
        {
            FileName = exe,
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            WorkingDirectory = Path.GetDirectoryName(exe) ?? Environment.CurrentDirectory
        };
        foreach (var argument in new[] { "tls", "configure", "--hostname", hostname, "--public-url", publicUrl,
                     "--certificate-pfx", pfxPath, "--certificate-pfx-password-stdin", "--install-dir", AppContext.BaseDirectory })
            psi.ArgumentList.Add(argument);
        try
        {
            using var process = Process.Start(psi)
                ?? throw new InvalidOperationException("failed to start detached tls configure process");
            await process.StandardInput.WriteLineAsync((pfxPassword ?? "").AsMemory(), cancellationToken);
            process.StandardInput.Close();
        }
        catch (Exception)
        {
            op.Status = CertificateRenewalStatus.Failed;
            op.ErrorMessage = "Unable to start certificate activation; inspect local TLS diagnostics";
            op.CompletedAt = op.UpdatedAt = _clock.UtcNow;
            using var persist = new CancellationTokenSource(TimeSpan.FromSeconds(10));
            await _db.SaveChangesAsync(persist.Token);
            throw;
        }

        return op;
    }

    private async Task TryCompleteAfterSwitchAsync(
        CertificateRenewalOperation op,
        CancellationToken cancellationToken)
    {
        try
        {
            var status = await _cpStatus.GetStatusAsync(cancellationToken).ConfigureAwait(false);
            if (!string.IsNullOrWhiteSpace(op.NewThumbprint) &&
                string.Equals(status.Thumbprint, op.NewThumbprint, StringComparison.OrdinalIgnoreCase) &&
                status.HasPrivateKey &&
                status.SystemTrustOk && await VerifyServedCertificateAsync(op, cancellationToken))
            {
                op.Status = CertificateRenewalStatus.Completed;
                op.UpdatedAt = _clock.UtcNow;
                op.CompletedAt = op.UpdatedAt;
                op.ErrorMessage = null;
                await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            }
        }
        catch (Exception) when (!cancellationToken.IsCancellationRequested)
        {
            // Retry within the bounded activation window.
        }
        if (op.Status == CertificateRenewalStatus.Switching && _clock.UtcNow - op.UpdatedAt > TimeSpan.FromMinutes(5))
        {
            op.Status = CertificateRenewalStatus.Failed;
            op.ErrorMessage = "Certificate activation not verified within five minutes; inspect TLS rollback diagnostics";
            op.CompletedAt = _clock.UtcNow;
            await _db.SaveChangesAsync(cancellationToken);
        }
    }

    private async Task<bool> VerifyServedCertificateAsync(CertificateRenewalOperation op, CancellationToken ct)
    {
        if (!int.TryParse(_configuration?["Hosting:Port"], out var port) || port is < 1 or > 65535)
            return false;
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
        timeout.CancelAfter(TimeSpan.FromSeconds(10));
        using var tcp = new System.Net.Sockets.TcpClient();
        await tcp.ConnectAsync("127.0.0.1", port, timeout.Token);
        using var tls = new System.Net.Security.SslStream(tcp.GetStream());
        await tls.AuthenticateAsClientAsync(new System.Net.Security.SslClientAuthenticationOptions
        {
            TargetHost = op.Domain,
            CertificateRevocationCheckMode = System.Security.Cryptography.X509Certificates.X509RevocationMode.Online
        }, timeout.Token);
        return tls.RemoteCertificate is not null && string.Equals(tls.RemoteCertificate.GetCertHashString(),
            op.NewThumbprint, StringComparison.OrdinalIgnoreCase);
    }

    private async Task<CertificateRenewalOperation> LoadAsync(Guid id, CancellationToken cancellationToken) =>
        await _db.CertificateRenewalOperations.FirstOrDefaultAsync(o => o.Id == id, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("certificate renewal operation not found");

    private static string? Truncate(string? value, int max) =>
        value is null ? null : (value.Length <= max ? value : value[..max]);

    private static string EscapeArg(string value) =>
        "\"" + value.Replace("\"", "\\\"", StringComparison.Ordinal) + "\"";
}

public interface IDnsTxtLookup
{
    Task<IReadOnlyList<string>> LookupTxtAsync(string name, CancellationToken cancellationToken = default);
}

public sealed class SystemDnsTxtLookup : IDnsTxtLookup
{
    public async Task<IReadOnlyList<string>> LookupTxtAsync(
        string name,
        CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        try
        {
            var lookup = new DnsClient.LookupClient();
            var result = await lookup.QueryAsync(name, DnsClient.QueryType.TXT, cancellationToken: cancellationToken)
                .ConfigureAwait(false);
            return result.Answers.TxtRecords()
                .Select(r => string.Join(string.Empty, r.Text))
                .Where(s => !string.IsNullOrWhiteSpace(s))
                .ToList();
        }
        catch
        {
            return Array.Empty<string>();
        }
    }
}

/// <summary>In-memory / staging-safe ACME provider for tests and Acme:UseFakeProvider.</summary>
public sealed class FakeAcmeDns01Provider : IAcmeDns01Provider
{
    public Task<AcmeDns01Order> CreateDns01OrderAsync(string domain, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var token = Convert.ToHexString(Guid.NewGuid().ToByteArray()).ToLowerInvariant();
        return Task.FromResult(new AcmeDns01Order
        {
            OrderUrl = $"fake://acme/order/{Guid.NewGuid():N}",
            ChallengeName = $"_acme-challenge.{domain}",
            ChallengeValue = token,
            ChallengeExpiresAt = DateTime.UtcNow.AddHours(1)
        });
    }

    public Task ValidateChallengeAsync(string orderUrl, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        if (string.IsNullOrWhiteSpace(orderUrl))
            throw new ValidationException("order url required");
        return Task.CompletedTask;
    }

    public Task<AcmeIssuedCertificate> FinalizeAsync(string orderUrl, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var thumb = Convert.ToHexString(Guid.NewGuid().ToByteArray()) + Convert.ToHexString(Guid.NewGuid().ToByteArray());
        return Task.FromResult(new AcmeIssuedCertificate
        {
            CertificatePem = "-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----",
            Thumbprint = thumb[..40],
            PfxPath = null
        });
    }
}
