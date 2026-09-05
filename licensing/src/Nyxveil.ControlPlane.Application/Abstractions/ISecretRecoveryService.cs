namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// Portable password-encrypted recovery for signing keys and License KEK (never SQL password).
/// </summary>
public interface ISecretRecoveryService
{
    /// <summary>Export signing keys + license KEK into a portable JSON recovery bundle.</summary>
    Task ExportRecoveryBundleAsync(
        Stream output,
        string password,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Import a recovery bundle. Does not overwrite an existing Current signing key unless <paramref name="force"/>.
    /// </summary>
    Task ImportRecoveryBundleAsync(
        Stream input,
        string password,
        bool force = false,
        CancellationToken cancellationToken = default);
}

/// <summary>Portable License KEK backup/restore (PBKDF2 + AES-256-GCM JSON).</summary>
public interface ILicenseKekBackupService
{
    Task ExportAsync(Stream output, string password, CancellationToken cancellationToken = default);

    Task ImportAsync(Stream input, string password, CancellationToken cancellationToken = default);
}
