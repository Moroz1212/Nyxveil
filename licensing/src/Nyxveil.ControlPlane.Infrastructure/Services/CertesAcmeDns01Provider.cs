using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using Certes;
using Certes.Acme;
using Certes.Acme.Resource;
using Microsoft.Extensions.Options;
using IoDirectory = System.IO.Directory;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

/// <summary>
/// Certes-backed ACME DNS-01. Account and leaf private keys stay under ProgramData — never in DB.
/// Order metadata (URLs, challenge token/value) is stored in a local JSON sidecar for wizard resume.
/// </summary>
public sealed class CertesAcmeDns01Provider : IAcmeDns01Provider
{
    private readonly AcmeOptions _options;
    private static readonly JsonSerializerOptions JsonOpts = new() { WriteIndented = true };

    public CertesAcmeDns01Provider(IOptions<AcmeOptions> options)
    {
        _options = options.Value;
    }

    public async Task<AcmeDns01Order> CreateDns01OrderAsync(
        string domain,
        CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        domain = domain.Trim().TrimEnd('.').ToLowerInvariant();
        if (string.IsNullOrWhiteSpace(domain))
            throw new ValidationException("domain is required");

        var acme = await CreateContextAsync(cancellationToken).ConfigureAwait(false);
        var order = await acme.NewOrder(new[] { domain }).ConfigureAwait(false);
        var authz = (await order.Authorizations().ConfigureAwait(false)).First();
        var dns = await authz.Dns().ConfigureAwait(false)
                  ?? throw new InvalidOperationException("ACME order has no DNS-01 challenge");

        var txt = acme.AccountKey.DnsTxt(dns.Token);
        var orderUrl = order.Location?.AbsoluteUri
                       ?? throw new InvalidOperationException("ACME order location missing");

        var state = new OrderState
        {
            OrderUrl = orderUrl,
            Domain = domain,
            ChallengeToken = dns.Token,
            ChallengeValue = txt,
            ChallengeName = "_acme-challenge." + domain,
            ChallengeExpiresAt = DateTime.UtcNow.AddHours(1),
            CreatedAt = DateTime.UtcNow
        };
        SaveOrderState(state);

        return new AcmeDns01Order
        {
            OrderUrl = orderUrl,
            ChallengeName = state.ChallengeName,
            ChallengeValue = txt,
            ChallengeExpiresAt = state.ChallengeExpiresAt
        };
    }

    public async Task ValidateChallengeAsync(string orderUrl, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var state = LoadOrderState(orderUrl)
                    ?? throw new ValidationException("ACME order state not found on disk; create a new request");

        var acme = await CreateContextAsync(cancellationToken).ConfigureAwait(false);
        var order = acme.Order(new Uri(state.OrderUrl));
        var authz = (await order.Authorizations().ConfigureAwait(false)).First();
        var dns = await authz.Dns().ConfigureAwait(false)
                  ?? throw new InvalidOperationException("DNS-01 challenge missing");

        await dns.Validate().ConfigureAwait(false);

        // Poll until valid or invalid (bounded).
        for (var i = 0; i < 30; i++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            await Task.Delay(TimeSpan.FromSeconds(2), cancellationToken).ConfigureAwait(false);
            var resource = await authz.Resource().ConfigureAwait(false);
            if (resource.Status == AuthorizationStatus.Valid)
                return;
            if (resource.Status == AuthorizationStatus.Invalid)
                throw new ValidationException("ACME DNS-01 challenge was rejected by the CA");
        }

        throw new ValidationException("ACME DNS-01 challenge validation timed out");
    }

    public async Task<AcmeIssuedCertificate> FinalizeAsync(
        string orderUrl,
        CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var state = LoadOrderState(orderUrl)
                    ?? throw new ValidationException("ACME order state not found on disk; create a new request");

        var acme = await CreateContextAsync(cancellationToken).ConfigureAwait(false);
        var order = acme.Order(new Uri(state.OrderUrl));

        var leafKey = KeyFactory.NewKey(KeyAlgorithm.ES256);
        var certChain = await order.Generate(
                new CsrInfo { CommonName = state.Domain },
                leafKey)
            .ConfigureAwait(false);

        var pfxPassword = Convert.ToHexString(RandomNumberGenerator.GetBytes(16));
        var pfxBytes = certChain.ToPfx(leafKey).Build(state.Domain, pfxPassword);

        using var cert = X509CertificateLoader.LoadPkcs12(
            pfxBytes,
            pfxPassword,
            X509KeyStorageFlags.Exportable | X509KeyStorageFlags.EphemeralKeySet);
        var thumb = cert.Thumbprint ?? throw new InvalidOperationException("issued certificate has no thumbprint");

        var dir = Path.Combine(WizardRoot(), "issued", thumb);
        IoDirectory.CreateDirectory(dir);
        var pfxPath = Path.Combine(dir, "cert.pfx");
        var pwdPath = Path.Combine(dir, "pfx.password");
        await File.WriteAllBytesAsync(pfxPath, pfxBytes, cancellationToken).ConfigureAwait(false);
        await File.WriteAllTextAsync(pwdPath, pfxPassword, cancellationToken).ConfigureAwait(false);

        state.PfxPath = pfxPath;
        state.PfxPasswordPath = pwdPath;
        state.Thumbprint = thumb;
        SaveOrderState(state);

        return new AcmeIssuedCertificate
        {
            CertificatePem = certChain.Certificate.ToPem(),
            Thumbprint = thumb,
            PfxPath = pfxPath
        };
    }

    /// <summary>Returns PFX path + password for an issued order (disk only).</summary>
    public static (string? PfxPath, string? Password) TryGetIssuedPfx(string? orderUrl, string? thumbprint)
    {
        if (!string.IsNullOrWhiteSpace(orderUrl))
        {
            var state = LoadOrderState(orderUrl);
            if (state?.PfxPath is not null && File.Exists(state.PfxPath))
            {
                var pwd = state.PfxPasswordPath is not null && File.Exists(state.PfxPasswordPath)
                    ? File.ReadAllText(state.PfxPasswordPath)
                    : null;
                return (state.PfxPath, pwd);
            }
        }

        if (!string.IsNullOrWhiteSpace(thumbprint))
        {
            var pfxPath = Path.Combine(WizardRoot(), "issued", thumbprint.Trim(), "cert.pfx");
            var pwdPath = Path.Combine(WizardRoot(), "issued", thumbprint.Trim(), "pfx.password");
            if (File.Exists(pfxPath))
            {
                var pwd = File.Exists(pwdPath) ? File.ReadAllText(pwdPath) : null;
                return (pfxPath, pwd);
            }
        }

        return (null, null);
    }

    private async Task<AcmeContext> CreateContextAsync(CancellationToken cancellationToken)
    {
        cancellationToken.ThrowIfCancellationRequested();
        var directory = _options.UseStaging
            ? WellKnownServers.LetsEncryptStagingV2
            : WellKnownServers.LetsEncryptV2;

        var keyPath = AccountKeyPath();
        IoDirectory.CreateDirectory(Path.GetDirectoryName(keyPath)!);

        IKey accountKey;
        if (File.Exists(keyPath))
        {
            accountKey = KeyFactory.FromPem(await File.ReadAllTextAsync(keyPath, cancellationToken).ConfigureAwait(false));
            return new AcmeContext(directory, accountKey);
        }

        accountKey = KeyFactory.NewKey(KeyAlgorithm.ES256);
        var acme = new AcmeContext(directory, accountKey);
        var email = string.IsNullOrWhiteSpace(_options.ContactEmail)
            ? "mailto:acme@" + Environment.MachineName
            : _options.ContactEmail.Trim();
        if (!email.StartsWith("mailto:", StringComparison.OrdinalIgnoreCase))
            email = "mailto:" + email;
        await acme.NewAccount(email, true).ConfigureAwait(false);

        await File.WriteAllTextAsync(keyPath, accountKey.ToPem(), cancellationToken).ConfigureAwait(false);
        return acme;
    }

    private string AccountKeyPath()
    {
        var relative = string.IsNullOrWhiteSpace(_options.AccountKeyRelativePath)
            ? "secrets/acme-account.pem"
            : _options.AccountKeyRelativePath;
        return Path.IsPathRooted(relative)
            ? relative
            : Path.Combine(TlsConfigPaths.ProgramDataRoot, relative);
    }

    private static string WizardRoot() =>
        Path.Combine(TlsConfigPaths.ProgramDataRoot, "acme-wizard");

    private static string OrderStatePath(string orderUrl)
    {
        var hash = Convert.ToHexString(SHA256.HashData(System.Text.Encoding.UTF8.GetBytes(orderUrl)))
            .ToLowerInvariant();
        return Path.Combine(WizardRoot(), "orders", hash + ".json");
    }

    private static void SaveOrderState(OrderState state)
    {
        var path = OrderStatePath(state.OrderUrl);
        IoDirectory.CreateDirectory(Path.GetDirectoryName(path)!);
        var tmp = path + ".tmp";
        File.WriteAllText(tmp, JsonSerializer.Serialize(state, JsonOpts));
        File.Move(tmp, path, overwrite: true);
    }

    private static OrderState? LoadOrderState(string orderUrl)
    {
        var path = OrderStatePath(orderUrl);
        if (!File.Exists(path))
            return null;
        return JsonSerializer.Deserialize<OrderState>(File.ReadAllText(path), JsonOpts);
    }

    private sealed class OrderState
    {
        public string OrderUrl { get; set; } = "";
        public string Domain { get; set; } = "";
        public string ChallengeToken { get; set; } = "";
        public string ChallengeValue { get; set; } = "";
        public string ChallengeName { get; set; } = "";
        public DateTime? ChallengeExpiresAt { get; set; }
        public DateTime CreatedAt { get; set; }
        public string? PfxPath { get; set; }
        public string? PfxPasswordPath { get; set; }
        public string? Thumbprint { get; set; }
    }
}
