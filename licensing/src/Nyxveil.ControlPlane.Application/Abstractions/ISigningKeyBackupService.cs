namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// Password-protected export/import of control-plane signing key material.
/// Portable bundles decrypt DPAPI on export and re-protect on the destination machine on import.
/// </summary>
public interface ISigningKeyBackupService
{
    /// <summary>Export portable JSON (PBKDF2 + AES-256-GCM) for all signing keys.</summary>
    Task ExportPortableAsync(Stream output, string password, CancellationToken cancellationToken = default);

    /// <summary>
    /// Import portable JSON. Does not overwrite Current keys unless <paramref name="force"/>.
    /// </summary>
    Task ImportPortableAsync(
        Stream input,
        string password,
        bool force = false,
        CancellationToken cancellationToken = default);

    /// <summary>Legacy ZIP wrapper that embeds the portable JSON payload.</summary>
    Task ExportEncryptedZipAsync(Stream output, string password, CancellationToken cancellationToken = default);

    Task ImportEncryptedZipAsync(
        Stream input,
        string password,
        bool force = false,
        CancellationToken cancellationToken = default);
}
