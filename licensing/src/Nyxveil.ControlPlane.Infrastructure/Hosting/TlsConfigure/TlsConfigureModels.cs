using System.Text.Json;
using System.Text.Json.Nodes;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

/// <summary>
/// PublicBaseUrl semantics (Control Plane 1.0.5 audit):
/// <list type="bullet">
/// <item>Stored in Hosting:PublicBaseUrl (appsettings.Production.json) and operational.json.</item>
/// <item>Operator-facing / externally advertised canonical URL (may include NAT port).</item>
/// <item>NOT consumed by C# runtime for Kestrel listen, catalog, redirects, node callbacks, or health probes.</item>
/// <item>Health / TLS probes use PublicHostname + local Hosting:Port (e.g. 8443), never PublicBaseUrl.</item>
/// <item>Therefore external NAT 18443→8443 must be reflected in PublicBaseUrl as :18443 while Port stays 8443.</item>
/// </list>
/// </summary>
public static class PublicBaseUrlSemantics
{
    public const string Documentation =
        "PublicBaseUrl is the externally advertised HTTPS base URL for operators. " +
        "It is not used by the Control Plane process for listening or API link generation. " +
        "Local listen remains Hosting:Port; probes use PublicHostname + Port.";

    /// <summary>
    /// Builds or validates an external public URL. Does not use the local Kestrel port unless the caller passes it.
    /// </summary>
    public static string NormalizeExternalPublicUrl(string publicUrl)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(publicUrl);
        if (!Uri.TryCreate(publicUrl.Trim(), UriKind.Absolute, out var uri) ||
            !string.Equals(uri.Scheme, Uri.UriSchemeHttps, StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException(
                "PublicBaseUrl / --public-url must be an absolute https:// URL (e.g. https://cp.nyxveil.ru:18443).");
        }

        if (string.IsNullOrWhiteSpace(uri.Host))
            throw new InvalidOperationException("PublicBaseUrl host is empty.");

        // Preserve explicit non-default ports (including 18443). Drop trailing slash.
        var builder = new UriBuilder(uri) { Path = "", Query = "", Fragment = "" };
        return builder.Uri.GetLeftPart(UriPartial.Authority).TrimEnd('/');
    }

    public static void AssertHostnameMatchesPublicUrl(string hostname, string publicUrl)
    {
        var url = NormalizeExternalPublicUrl(publicUrl);
        var uri = new Uri(url);
        if (!string.Equals(uri.Host, hostname.Trim().TrimEnd('.'), StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException(
                $"--hostname '{hostname}' does not match host in --public-url '{publicUrl}'.");
        }
    }

    public static int? TryGetExternalPort(string publicUrl)
    {
        var url = NormalizeExternalPublicUrl(publicUrl);
        var uri = new Uri(url);
        return uri.IsDefaultPort ? 443 : uri.Port;
    }
}

/// <summary>Inputs for <c>tls configure</c> (dry-run or apply).</summary>
public sealed record TlsConfigureRequest
{
    public string InstallDir { get; init; } = @"C:\Program Files\Nyxveil\ControlPlane";
    public string Hostname { get; init; } = "";
    public string PublicUrl { get; init; } = "";
    public string? CertificateThumbprint { get; init; }
    public string? CertificatePfxPath { get; init; }
    public string? CertificatePfxPassword { get; init; }
    public string ServiceName { get; init; } = "NyxveilControlPlane";
    public string ServiceAccount { get; init; } = @"NT SERVICE\NyxveilControlPlane";
    public bool CheckOnly { get; init; }
    public bool SkipServiceControl { get; init; }
    public bool SkipPrivateKeyAcl { get; init; }
    /// <summary>Test hook: validate PFX and write thumbprint without importing into LocalMachine\\My.</summary>
    public bool SkipPfxStoreImport { get; init; }
    public bool RequireSystemTrust { get; init; } = true;
    /// <summary>Forbidden local listen ports (NAT external port included).</summary>
    public int[] ForbiddenListenPorts { get; init; } = [80, 443, 18443];
    public TimeSpan HealthTimeout { get; init; } = TimeSpan.FromSeconds(60);
    public Func<CancellationToken, Task<(bool Ok, string Message)>>? HealthProbeAsync { get; init; }
}

public sealed class TlsConfigureSnapshot
{
    public required string AppsettingsProductionPath { get; init; }
    public required string OperationalProgramDataPath { get; init; }
    public string? OperationalInstallCopyPath { get; init; }
    public required string AppsettingsJson { get; init; }
    public required string OperationalJson { get; init; }
    public string? OperationalInstallCopyJson { get; init; }
    public int Port { get; init; }
    public string PublicHostname { get; init; } = "";
    public string PublicBaseUrl { get; init; } = "";
    public string CertificateMode { get; init; } = "";
    public string CertificateValidationMode { get; init; } = "";
    public string CertificateThumbprint { get; init; } = "";
    public string FirewallRuleName { get; init; } = "";
    public string ServiceName { get; init; } = "";
}

public sealed class TlsConfigureResult
{
    public bool DryRun { get; init; }
    public bool Success { get; init; }
    public bool RolledBack { get; init; }
    public string Message { get; init; } = "";
    public int PreservedPort { get; init; }
    public string PreviousThumbprint { get; init; } = "";
    public string NewThumbprint { get; init; } = "";
    public string PreviousHostname { get; init; } = "";
    public string NewHostname { get; init; } = "";
    public string PreviousPublicBaseUrl { get; init; } = "";
    public string NewPublicBaseUrl { get; init; } = "";
    public string PreviousValidationMode { get; init; } = "";
    public string NewValidationMode { get; init; } = "SystemTrust";
    public IReadOnlyList<string> IntendedChanges { get; init; } = Array.Empty<string>();
}

public sealed record TlsStatusReport
{
    public int LocalListenPort { get; init; }
    public string PublicHostname { get; init; } = "";
    public string PublicBaseUrl { get; init; } = "";
    public string CertificateMode { get; init; } = "";
    public string ValidationMode { get; init; } = "";
    public string Thumbprint { get; init; } = "";
    public string Subject { get; init; } = "";
    public string San { get; init; } = "";
    public string Issuer { get; init; } = "";
    public DateTimeOffset? NotBefore { get; init; }
    public DateTimeOffset? NotAfter { get; init; }
    public bool HasPrivateKey { get; init; }
    public string PublicTrustResult { get; init; } = "";
    public string ServiceAccountPrivateKeyAccess { get; init; } = "";
    public string PublicBaseUrlNote { get; init; } = PublicBaseUrlSemantics.Documentation;
}

public static class TlsConfigPaths
{
    /// <summary>Test hook — set to a temp directory; null uses real CommonApplicationData.</summary>
    public static string? ProgramDataRootOverride { get; set; }

    public static string ProgramDataRoot =>
        ProgramDataRootOverride
        ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "Nyxveil", "ControlPlane");

    public static string OperationalProgramData => Path.Combine(ProgramDataRoot, "operational.json");

    public static string AppsettingsProduction(string installDir) =>
        Path.Combine(installDir, "appsettings.Production.json");

    /// <summary>
    /// Install mirror only — never nest under config\config. Callers must pass the install root,
    /// not an already-nested config directory.
    /// </summary>
    public static string OperationalInstallCopy(string installDir) =>
        Path.Combine(installDir, "config", "operational.json");

    public static void AssertNotNestedConfigDir(string installDir)
    {
        var full = Path.GetFullPath(installDir).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        if (string.Equals(Path.GetFileName(full), "config", StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException(
                "InstallDir must be the Control Plane root (e.g. ...\\ControlPlane), not ...\\config.");
        }
    }
}

public static class TlsJsonConfig
{
    private static readonly JsonSerializerOptions WriteOptions = new()
    {
        WriteIndented = true
    };

    public static JsonObject LoadObject(string path)
    {
        var raw = File.ReadAllText(path);
        var node = JsonNode.Parse(raw) as JsonObject
                   ?? throw new InvalidOperationException("Invalid JSON object: " + path);
        return node;
    }

    public static void AtomicWrite(string path, JsonObject obj)
    {
        var dir = Path.GetDirectoryName(path);
        if (!string.IsNullOrEmpty(dir))
            Directory.CreateDirectory(dir);

        var tmp = path + ".tmp";
        var json = obj.ToJsonString(WriteOptions);
        File.WriteAllText(tmp, json);
        File.Move(tmp, path, overwrite: true);
    }

    public static int ReadPort(JsonObject appsettingsOrOperational)
    {
        if (appsettingsOrOperational["Hosting"] is JsonObject hosting &&
            hosting["Port"] is JsonValue pv &&
            pv.TryGetValue<int>(out var port))
            return port;

        if (appsettingsOrOperational["Port"] is JsonValue op &&
            op.TryGetValue<int>(out var port2))
            return port2;

        return 0;
    }

    public static string ReadString(JsonObject root, params string[] path)
    {
        JsonNode? cur = root;
        foreach (var p in path)
        {
            if (cur is not JsonObject o || !o.TryGetPropertyValue(p, out cur) || cur is null)
                return "";
        }

        return cur.GetValue<string>() ?? "";
    }
}
