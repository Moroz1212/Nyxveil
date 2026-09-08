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
        if (op.Status is CertificateRenewalStatus.Completed or CertificateRenewalStatus.Cancelled)
            throw new ConflictException("operation already finished");

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
        catch (Exception ex) when (ex is not Nyxveil.ControlPlane.Application.Exceptions.ApplicationException)
        {
            op.Status = CertificateRenewalStatus.Failed;
            op.ErrorMessage = Truncate(ex.Message, 1024);
            op.UpdatedAt = _clock.UtcNow;
            op.CompletedAt = op.UpdatedAt;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
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
            // Fake/dev provider: durable wizard completes without Windows Store switch.
            op.Status = CertificateRenewalStatus.Completed;
            op.UpdatedAt = _clock.UtcNow;
            op.CompletedAt = op.UpdatedAt;
            op.ErrorMessage = "PFX not present (fake ACME); Store import skipped";
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            return op;
        }

        var hostname = op.Domain;
        var publicUrl = (_configuration?["Hosting:PublicBaseUrl"] ?? "").Trim();
        if (string.IsNullOrWhiteSpace(publicUrl))
            publicUrl = "https://" + hostname;

        var exe = Environment.ProcessPath
                   ?? throw new InvalidOperationException("process path unavailable for detached tls configure");
        var args =
            $"tls configure --hostname {EscapeArg(hostname)} --public-url {EscapeArg(publicUrl)} " +
            $"--certificate-pfx {EscapeArg(pfxPath)}";
        if (!string.IsNullOrEmpty(pfxPassword))
            args += $" --certificate-pfx-password {EscapeArg(pfxPassword)}";

        op.Status = CertificateRenewalStatus.Switching;
        op.UpdatedAt = _clock.UtcNow;
        op.ErrorMessage = null;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);

        // Detached local maintenance process reuses existing TlsConfigureEngine via CLI.
        var psi = new ProcessStartInfo
        {
            FileName = exe,
            Arguments = args,
            UseShellExecute = false,
            CreateNoWindow = true,
            WorkingDirectory = Path.GetDirectoryName(exe) ?? Environment.CurrentDirectory
        };
        _ = Process.Start(psi)
            ?? throw new InvalidOperationException("failed to start detached tls configure process");

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
                status.SystemTrustOk)
            {
                op.Status = CertificateRenewalStatus.Completed;
                op.UpdatedAt = _clock.UtcNow;
                op.CompletedAt = op.UpdatedAt;
                op.ErrorMessage = null;
                await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            }
        }
        catch
        {
            // Keep Switching until post-verify succeeds or operator cancels.
        }
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
