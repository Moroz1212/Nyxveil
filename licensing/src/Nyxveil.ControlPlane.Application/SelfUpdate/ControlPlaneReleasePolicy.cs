using System.IO.Compression;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using Nyxveil.ControlPlane.Application.Common;

namespace Nyxveil.ControlPlane.Application.SelfUpdate;

/// <summary>Pure helpers for CP self-update discovery and package safety (unit-testable).</summary>
public static class ControlPlaneReleasePolicy
{
    private static readonly Regex TagPattern = new(
        @"^control-plane-v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$",
        RegexOptions.IgnoreCase | RegexOptions.CultureInvariant | RegexOptions.Compiled);

    public static bool TryParseTag(string? tag, out string version)
    {
        version = "";
        if (string.IsNullOrWhiteSpace(tag)) return false;
        var m = TagPattern.Match(tag.Trim());
        if (!m.Success) return false;
        version = m.Groups[1].Value;
        return SemVersion.TryParse(version, out _);
    }

    public static string ExpectedPackageName(string version) =>
        $"Nyxveil-ControlPlane-v{version}-release.zip";

    public static string ExpectedChecksumName(string version) =>
        $"Nyxveil-ControlPlane-v{version}-release.zip.sha256";

    public static ControlPlaneUpdateAvailability Compare(string? installed, string? latest)
    {
        if (!SemVersion.TryParse(installed, out var cur) || !SemVersion.TryParse(latest, out var lat))
            return ControlPlaneUpdateAvailability.Unknown;
        var c = cur.CompareTo(lat);
        if (c < 0) return ControlPlaneUpdateAvailability.UpdateAvailable;
        if (c == 0) return ControlPlaneUpdateAvailability.Current;
        return ControlPlaneUpdateAvailability.InstalledNewer;
    }

    public static string StatusLabelRu(ControlPlaneUpdateAvailability a) => a switch
    {
        ControlPlaneUpdateAvailability.Current => "Актуально",
        ControlPlaneUpdateAvailability.UpdateAvailable => "Доступно обновление",
        ControlPlaneUpdateAvailability.InstalledNewer => "Установленная версия новее опубликованной.",
        _ => "Статус неизвестен"
    };

    public static bool IsAcceptableRelease(bool draft, bool prerelease, bool allowPrerelease) =>
        !draft && (allowPrerelease || !prerelease);

    public static string? ParseSha256Sidecar(string content)
    {
        if (string.IsNullOrWhiteSpace(content)) return null;
        var line = content.Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .FirstOrDefault();
        if (line is null) return null;
        var token = line.Split(' ', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .FirstOrDefault();
        if (token is null || token.Length != 64) return null;
        return token.All(Uri.IsHexDigit) ? token.ToUpperInvariant() : null;
    }

    public static string Sha256Hex(Stream stream)
    {
        using var sha = SHA256.Create();
        var hash = sha.ComputeHash(stream);
        return Convert.ToHexString(hash);
    }

    public static string Sha256Hex(byte[] bytes)
    {
        using var sha = SHA256.Create();
        return Convert.ToHexString(sha.ComputeHash(bytes));
    }

    /// <summary>Reject zip-slip and absolute/empty entries before extraction.</summary>
    public static void AssertSafeZipEntries(string zipPath, string destinationRoot)
    {
        var root = Path.GetFullPath(destinationRoot).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
                   + Path.DirectorySeparatorChar;
        using var archive = ZipFile.OpenRead(zipPath);
        foreach (var entry in archive.Entries)
        {
            if (string.IsNullOrEmpty(entry.FullName) || entry.FullName.EndsWith('/') || entry.FullName.EndsWith('\\'))
                continue;
            var name = entry.FullName.Replace('\\', '/');
            if (name.StartsWith('/') || name.Contains("..", StringComparison.Ordinal) ||
                name.Contains(':', StringComparison.Ordinal))
                throw new InvalidOperationException("unsafe archive entry: " + entry.FullName);

            var dest = Path.GetFullPath(Path.Combine(destinationRoot, name.Replace('/', Path.DirectorySeparatorChar)));
            if (!dest.StartsWith(root, StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("zip-slip blocked: " + entry.FullName);
        }
    }

    public static void ExtractZipSafe(string zipPath, string destinationRoot)
    {
        AssertSafeZipEntries(zipPath, destinationRoot);
        Directory.CreateDirectory(destinationRoot);
        ZipFile.ExtractToDirectory(zipPath, destinationRoot, overwriteFiles: true);
    }

    public static bool PackageVersionMatches(string extractedRoot, string expectedVersion)
    {
        var versionFile = Path.Combine(extractedRoot, "VERSION");
        if (!File.Exists(versionFile)) return false;
        var v = File.ReadAllText(versionFile).Trim();
        return string.Equals(v, expectedVersion, StringComparison.Ordinal);
    }

    public static string BuildHandoffJson(SelfUpdateTransaction tx, string installDir, string serviceName) =>
        """
        {
          "transactionId": "%TX%",
          "currentVersion": "%CUR%",
          "targetVersion": "%TAR%",
          "releaseTag": "%TAG%",
          "packageSha256": "%SHA%",
          "stagingPath": "%STG%",
          "backupPath": "%BKP%",
          "installDir": "%INS%",
          "serviceName": "%SVC%"
        }
        """
            .Replace("%TX%", tx.TransactionId.ToString("N"), StringComparison.Ordinal)
            .Replace("%CUR%", Escape(tx.CurrentVersion), StringComparison.Ordinal)
            .Replace("%TAR%", Escape(tx.TargetVersion), StringComparison.Ordinal)
            .Replace("%TAG%", Escape(tx.ReleaseTag), StringComparison.Ordinal)
            .Replace("%SHA%", Escape(tx.PackageSha256), StringComparison.Ordinal)
            .Replace("%STG%", Escape(tx.StagingPath ?? ""), StringComparison.Ordinal)
            .Replace("%BKP%", Escape(tx.BackupPath ?? ""), StringComparison.Ordinal)
            .Replace("%INS%", Escape(installDir), StringComparison.Ordinal)
            .Replace("%SVC%", Escape(serviceName), StringComparison.Ordinal);

    private static string Escape(string s) =>
        s.Replace("\\", "\\\\", StringComparison.Ordinal).Replace("\"", "\\\"", StringComparison.Ordinal);
}
