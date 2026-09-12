using System.Text.Json;

namespace Nyxveil.ControlPlane.Application.SelfUpdate;

/// <summary>
/// Canonical path / identity checks for privileged Control Plane self-update handoff.
/// Web may only request updates against fixed Nyxveil locations — never arbitrary paths.
/// </summary>
public static class PrivilegedUpdaterContract
{
    public const string UpdaterServiceName = "NyxveilControlPlaneUpdater";
    public const string ControlPlaneServiceName = "NyxveilControlPlane";
    public const string RequestFileName = "request.json";
    public const string HandoffFileName = "handoff.json";
    public const string ResultFileName = "result.json";

    public static string DefaultInstallDir =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "Nyxveil", "ControlPlane");

    public static string DefaultProgramDataRoot =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "Nyxveil", "ControlPlane");

    public static string SelfUpdateRoot(string? programDataRoot = null) =>
        Path.Combine(programDataRoot ?? DefaultProgramDataRoot, "self-update");

    public static bool IsUnder(string candidate, string root)
    {
        try
        {
            var full = Path.GetFullPath(candidate)
                .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
            var rootFull = Path.GetFullPath(root)
                .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar)
                + Path.DirectorySeparatorChar;
            return (full + Path.DirectorySeparatorChar)
                .StartsWith(rootFull, StringComparison.OrdinalIgnoreCase);
        }
        catch
        {
            return false;
        }
    }

    public static void AssertCanonicalHandoff(
        string installDir,
        string stagingPath,
        string backupPath,
        string serviceName,
        string? programDataRoot = null,
        string? expectedInstallDir = null)
    {
        if (!string.Equals(serviceName, ControlPlaneServiceName, StringComparison.Ordinal))
            throw new InvalidOperationException("handoff serviceName must be NyxveilControlPlane");

        var expectedInstall = Path.GetFullPath(expectedInstallDir ?? DefaultInstallDir)
            .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        var install = Path.GetFullPath(installDir)
            .TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        if (!string.Equals(install, expectedInstall, StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("handoff installDir is not the canonical Control Plane install path");

        var pd = programDataRoot ?? DefaultProgramDataRoot;
        var selfUpdate = SelfUpdateRoot(pd);
        if (!IsUnder(stagingPath, Path.Combine(selfUpdate, "staging")) &&
            !IsUnder(stagingPath, selfUpdate))
            throw new InvalidOperationException("handoff stagingPath escapes self-update staging root");

        if (!IsUnder(backupPath, Path.Combine(selfUpdate, "backups")) &&
            !IsUnder(backupPath, selfUpdate))
            throw new InvalidOperationException("handoff backupPath escapes self-update backups root");

        // Reject path traversal markers that survived normalization checks.
        foreach (var p in new[] { installDir, stagingPath, backupPath })
        {
            if (p.Contains("..", StringComparison.Ordinal) ||
                p.Contains('\0') ||
                p.StartsWith("\\\\", StringComparison.Ordinal))
                throw new InvalidOperationException("handoff path rejected");
        }
    }

    public static string BuildRequestJson(Guid transactionId) =>
        JsonSerializer.Serialize(new Dictionary<string, string>
        {
            ["transactionId"] = transactionId.ToString("N"),
            ["at"] = DateTime.UtcNow.ToString("O")
        });

    public static bool TryParseResult(string json, out SelfUpdateApplyResult result)
    {
        result = new SelfUpdateApplyResult();
        try
        {
            using var doc = JsonDocument.Parse(json);
            var root = doc.RootElement;
            result.ResultCode = root.TryGetProperty("resultCode", out var c) ? c.GetString() ?? "" : "";
            result.Version = root.TryGetProperty("version", out var v) ? v.GetString() ?? "" : "";
            result.Message = root.TryGetProperty("message", out var m) ? m.GetString() ?? "" : "";
            result.PrimaryFailure = root.TryGetProperty("primaryFailure", out var pf) ? pf.GetString() : null;
            result.RollbackAttempted = root.TryGetProperty("rollbackAttempted", out var ra) && ra.ValueKind is JsonValueKind.True;
            if (root.TryGetProperty("rollbackSucceeded", out var rs) && rs.ValueKind is JsonValueKind.True or JsonValueKind.False)
                result.RollbackSucceeded = rs.GetBoolean();
            result.RollbackFailure = root.TryGetProperty("rollbackFailure", out var rf) ? rf.GetString() : null;
            result.TransactionId = root.TryGetProperty("transactionId", out var tx) ? tx.GetString() : null;
            return !string.IsNullOrWhiteSpace(result.ResultCode);
        }
        catch
        {
            return false;
        }
    }
}

public sealed class SelfUpdateApplyResult
{
    public string ResultCode { get; set; } = "";
    public string Version { get; set; } = "";
    public string Message { get; set; } = "";
    public string? PrimaryFailure { get; set; }
    public bool RollbackAttempted { get; set; }
    public bool? RollbackSucceeded { get; set; }
    public string? RollbackFailure { get; set; }
    public string? TransactionId { get; set; }
}
