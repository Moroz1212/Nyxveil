using System.Security.Cryptography;
using System.Text;
using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using ProtectedData = System.Security.Cryptography.ProtectedData;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Portable PBKDF2 + AES-256-GCM backup/restore for License KEK (DPAPI file on this machine).
/// </summary>
public sealed class LicenseKekBackupService : ILicenseKekBackupService
{
    private readonly IConfiguration _configuration;

    public LicenseKekBackupService(IConfiguration configuration)
    {
        _configuration = configuration;
    }

    public Task ExportAsync(Stream output, string password, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(output);
        ArgumentException.ThrowIfNullOrEmpty(password);

        var kekHex = ResolveKekHex()
                     ?? throw new InvalidOperationException(
                         "License KEK not found in configuration or DPAPI secrets.");

        var plain = Encoding.UTF8.GetBytes(kekHex);
        try
        {
            var encrypted = PortableSecretCrypto.Encrypt(plain, password);
            var bundle = new PortableKekBundle
            {
                FormatVersion = 1,
                CreatedAt = DateTimeOffset.UtcNow,
                Algorithm = "LicenseKekHex",
                SaltB64 = Convert.ToBase64String(encrypted.Salt),
                Iterations = encrypted.Iterations,
                NonceB64 = Convert.ToBase64String(encrypted.Nonce),
                CiphertextB64 = Convert.ToBase64String(encrypted.Ciphertext),
                TagB64 = Convert.ToBase64String(encrypted.Tag)
            };

            var json = Encoding.UTF8.GetBytes(PortableSecretCrypto.ToJson(bundle));
            return output.WriteAsync(json, cancellationToken).AsTask();
        }
        finally
        {
            CryptographicOperations.ZeroMemory(plain);
        }
    }

    public async Task ImportAsync(Stream input, string password, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(input);
        ArgumentException.ThrowIfNullOrEmpty(password);

        using var ms = new MemoryStream();
        await input.CopyToAsync(ms, cancellationToken).ConfigureAwait(false);
        var bundle = PortableSecretCrypto.FromJson<PortableKekBundle>(ms.ToArray());

        var blob = new PortableSecretCrypto.EncryptedBlob(
            Convert.FromBase64String(bundle.SaltB64),
            bundle.Iterations,
            Convert.FromBase64String(bundle.NonceB64),
            Convert.FromBase64String(bundle.CiphertextB64),
            Convert.FromBase64String(bundle.TagB64));

        var plain = PortableSecretCrypto.Decrypt(blob, password);
        try
        {
            var kekHex = Encoding.UTF8.GetString(plain).Trim();
            if (kekHex.Length != 64 || !kekHex.All(Uri.IsHexDigit))
                throw new CryptographicException("Decrypted License KEK is not 64 hex characters.");

            WriteKekDpapi(kekHex);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(plain);
        }
    }

    private string? ResolveKekHex()
    {
        var fromConfig = _configuration["Security:LicenseKekHex"];
        if (!string.IsNullOrWhiteSpace(fromConfig))
            return fromConfig.Trim();

        var path = GetKekFilePath();
        if (!OperatingSystem.IsWindows() || !File.Exists(path))
            return null;

        var protectedBytes = File.ReadAllBytes(path);
        var plain = ProtectedData.Unprotect(protectedBytes, optionalEntropy: null, DataProtectionScope.LocalMachine);
        return Encoding.UTF8.GetString(plain).TrimEnd('\0', '\r', '\n');
    }

    public static string GetKekFilePath()
    {
        var programData = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
            "Nyxveil",
            "ControlPlane",
            "secrets",
            DpapiSecretsConfigurationProvider.LicenseKekFileName);
        return programData;
    }

    public static void WriteKekDpapi(string kekHex)
    {
        if (!OperatingSystem.IsWindows())
            throw new PlatformNotSupportedException("License KEK DPAPI files require Windows.");

        var path = GetKekFilePath();
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        var bytes = Encoding.UTF8.GetBytes(kekHex);
        try
        {
            var protectedBytes = ProtectedData.Protect(bytes, optionalEntropy: null, DataProtectionScope.LocalMachine);
            File.WriteAllBytes(path, protectedBytes);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(bytes);
        }
    }

    /// <summary>
    /// Creates a new random KEK DPAPI file only if missing. Returns true when created.
    /// </summary>
    public static bool EnsureKekExists()
    {
        if (!OperatingSystem.IsWindows())
            throw new PlatformNotSupportedException("License KEK DPAPI files require Windows.");

        var path = GetKekFilePath();
        if (File.Exists(path) && new FileInfo(path).Length > 0)
            return false;

        var kek = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();
        WriteKekDpapi(kek);
        return true;
    }
}
