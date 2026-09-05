using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Nyxveil.ControlPlane.Application.Abstractions;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Unified portable recovery: signing keys + license_kek. Never includes SQL password.
/// </summary>
public sealed class ControlPlaneRecoveryService : ISecretRecoveryService
{
    private readonly ISigningKeyBackupService _signingKeys;
    private readonly ILicenseKekBackupService _kekBackup;

    public ControlPlaneRecoveryService(
        ISigningKeyBackupService signingKeys,
        ILicenseKekBackupService kekBackup)
    {
        _signingKeys = signingKeys;
        _kekBackup = kekBackup;
    }

    public async Task ExportRecoveryBundleAsync(
        Stream output,
        string password,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(output);
        ArgumentException.ThrowIfNullOrEmpty(password);

        using var keysMs = new MemoryStream();
        await _signingKeys.ExportPortableAsync(keysMs, password, cancellationToken).ConfigureAwait(false);
        keysMs.Position = 0;
        var keysDoc = PortableSecretCrypto.FromJson<SigningKeysEnvelope>(keysMs.ToArray());

        PortableKekBundle? kek = null;
        try
        {
            using var kekMs = new MemoryStream();
            await _kekBackup.ExportAsync(kekMs, password, cancellationToken).ConfigureAwait(false);
            kekMs.Position = 0;
            kek = PortableSecretCrypto.FromJson<PortableKekBundle>(kekMs.ToArray());
        }
        catch (InvalidOperationException)
        {
            // KEK optional when exporting from a host that has not provisioned it yet.
        }

        var bundle = new ControlPlaneRecoveryBundle
        {
            FormatVersion = 1,
            CreatedAt = DateTimeOffset.UtcNow,
            SigningKeys = keysDoc.Keys,
            LicenseKek = kek
        };

        var json = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(bundle));
        await output.WriteAsync(json, cancellationToken).ConfigureAwait(false);

        // Ensure the serialized document never embeds plaintext secrets outside ciphertext fields.
        AssertNoPlaintextSecrets(json);
    }

    public async Task ImportRecoveryBundleAsync(
        Stream input,
        string password,
        bool force = false,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(input);
        ArgumentException.ThrowIfNullOrEmpty(password);

        using var ms = new MemoryStream();
        await input.CopyToAsync(ms, cancellationToken).ConfigureAwait(false);
        var bytes = ms.ToArray();
        AssertNoPlaintextSecrets(bytes);

        var bundle = PortableSecretCrypto.FromJson<ControlPlaneRecoveryBundle>(bytes);

        using (var keysMs = new MemoryStream())
        {
            var keysDoc = new SigningKeysEnvelope
            {
                FormatVersion = bundle.FormatVersion,
                CreatedAt = bundle.CreatedAt,
                Keys = bundle.SigningKeys
            };
            var keysJson = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(keysDoc));
            await keysMs.WriteAsync(keysJson, cancellationToken).ConfigureAwait(false);
            keysMs.Position = 0;
            await _signingKeys.ImportPortableAsync(keysMs, password, force, cancellationToken)
                .ConfigureAwait(false);
        }

        if (bundle.LicenseKek is not null)
        {
            using var kekMs = new MemoryStream();
            var kekJson = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(bundle.LicenseKek));
            await kekMs.WriteAsync(kekJson, cancellationToken).ConfigureAwait(false);
            kekMs.Position = 0;
            await _kekBackup.ImportAsync(kekMs, password, cancellationToken).ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Guardrail: recovery JSON must not contain known plaintext secret markers
    /// (SQL password fields, unprotected KEK hex outside ciphertext).
    /// </summary>
    public static void AssertNoPlaintextSecrets(ReadOnlySpan<byte> utf8Json)
    {
        var text = Encoding.UTF8.GetString(utf8Json);
        if (text.Contains("SqlPassword", StringComparison.OrdinalIgnoreCase) ||
            text.Contains("\"Password\"", StringComparison.OrdinalIgnoreCase) &&
            text.Contains("ConnectionString", StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException("Recovery bundle must not contain SQL password material.");
        }

        // Disallow a top-level plaintext LicenseKekHex field (ciphertext-only is OK).
        using var doc = JsonDocument.Parse(text);
        if (doc.RootElement.TryGetProperty("license_kek_hex", out _) ||
            doc.RootElement.TryGetProperty("LicenseKekHex", out _))
        {
            throw new InvalidOperationException("Recovery bundle must not contain plaintext LicenseKekHex.");
        }
    }

    private sealed class SigningKeysEnvelope
    {
        public int FormatVersion { get; set; } = 1;
        public DateTimeOffset CreatedAt { get; set; }
        public List<PortableKeyBundle> Keys { get; set; } = new();
    }
}
