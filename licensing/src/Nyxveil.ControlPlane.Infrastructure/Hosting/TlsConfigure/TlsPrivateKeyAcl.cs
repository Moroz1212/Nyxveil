using System.Diagnostics;
using System.Runtime.Versioning;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Security.Principal;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting.TlsConfigure;

public interface ITlsPrivateKeyAcl
{
    void GrantRead(X509Certificate2 certificate, string account);
    bool HasReadAccess(X509Certificate2 certificate, string account);
}

/// <summary>Windows LocalMachine private-key ACL grant (RSA CSP/CNG + ECDSA CNG).</summary>
[SupportedOSPlatform("windows")]
public sealed class WindowsTlsPrivateKeyAcl : ITlsPrivateKeyAcl
{
    public void GrantRead(X509Certificate2 certificate, string account)
    {
        var keyPath = ResolvePrivateKeyPath(certificate);
        var sid = (SecurityIdentifier)new NTAccount(account).Translate(typeof(SecurityIdentifier));

        var security = new FileSecurity(keyPath, AccessControlSections.Access);
        security.AddAccessRule(new FileSystemAccessRule(
            sid,
            FileSystemRights.Read,
            AccessControlType.Allow));
        new FileInfo(keyPath).SetAccessControl(security);

        if (!HasReadAccess(certificate, account))
            throw new InvalidOperationException("Private-key Read ACE was not persisted for " + account);
    }

    public bool HasReadAccess(X509Certificate2 certificate, string account)
    {
        var keyPath = ResolvePrivateKeyPath(certificate);
        var sid = (SecurityIdentifier)new NTAccount(account).Translate(typeof(SecurityIdentifier));
        var security = new FileSecurity(keyPath, AccessControlSections.Access);
        foreach (FileSystemAccessRule rule in security.GetAccessRules(true, true, typeof(SecurityIdentifier)))
        {
            if (rule.IdentityReference == sid &&
                rule.AccessControlType == AccessControlType.Allow &&
                (rule.FileSystemRights & FileSystemRights.Read) == FileSystemRights.Read)
            {
                return true;
            }
        }

        return false;
    }

    public static bool HasBroadIdentityAce(X509Certificate2 certificate)
    {
        var keyPath = ResolvePrivateKeyPath(certificate);
        var security = new FileSecurity(keyPath, AccessControlSections.Access);
        var everyone = new SecurityIdentifier(WellKnownSidType.WorldSid, null);
        var users = new SecurityIdentifier(WellKnownSidType.BuiltinUsersSid, null);

        foreach (FileSystemAccessRule rule in security.GetAccessRules(true, true, typeof(SecurityIdentifier)))
        {
            if (rule.AccessControlType != AccessControlType.Allow)
                continue;
            if (rule.IdentityReference.Equals(everyone) || rule.IdentityReference.Equals(users))
                return true;
        }

        return false;
    }

    internal static string ResolvePrivateKeyPath(X509Certificate2 certificate)
    {
        using var rsa = certificate.GetRSAPrivateKey();
        if (rsa is RSACng rsaCng)
            return ResolveCngPath(rsaCng.Key);
        if (rsa is RSACryptoServiceProvider rsaCsp)
        {
            var info = rsaCsp.CspKeyContainerInfo;
            return ResolveLegacyCspPath(info.UniqueKeyContainerName, info.MachineKeyStore);
        }

        using var ecdsa = certificate.GetECDsaPrivateKey();
        if (ecdsa is ECDsaCng ecdsaCng)
            return ResolveCngPath(ecdsaCng.Key);

        throw new InvalidOperationException(
            "Certificate does not expose a supported persisted RSA or ECDSA private key path.");
    }

    private static string ResolveCngPath(CngKey key)
    {
        var unique = key.UniqueName
                     ?? throw new InvalidOperationException("CNG key UniqueName is null (ephemeral key?).");
        var candidates = new[]
        {
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
                "Microsoft", "Crypto", "Keys", unique),
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
                "Microsoft", "Crypto", "SystemKeys", unique),
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
                "Microsoft", "Crypto", "Keys", unique)
        };
        var found = candidates.Where(File.Exists).Distinct(StringComparer.OrdinalIgnoreCase).ToArray();
        if (found.Length != 1)
        {
            throw new InvalidOperationException(
                $"Expected one CNG key file for '{unique}'; found {found.Length}.");
        }

        return found[0];
    }

    private static string ResolveLegacyCspPath(string uniqueName, bool machineKey)
    {
        var root = machineKey
            ? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
                "Microsoft", "Crypto", "RSA", "MachineKeys")
            : Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
                "Microsoft", "Crypto", "RSA");
        var path = Path.Combine(root, uniqueName);
        if (!File.Exists(path))
            throw new FileNotFoundException("CSP RSA machine key file not found.", path);
        return path;
    }
}

public sealed class RecordingTlsPrivateKeyAcl : ITlsPrivateKeyAcl
{
    public List<(string Thumbprint, string Account)> Grants { get; } = new();
    public HashSet<string> ReadAccounts { get; } = new(StringComparer.OrdinalIgnoreCase);

    public void GrantRead(X509Certificate2 certificate, string account)
    {
        Grants.Add((certificate.Thumbprint, account));
        ReadAccounts.Add(account);
    }

    public bool HasReadAccess(X509Certificate2 certificate, string account) =>
        ReadAccounts.Contains(account);
}

public interface ITlsServiceController
{
    string? Status(string serviceName);
    void Stop(string serviceName);
    void Start(string serviceName);
}

/// <summary>Service control via <c>sc.exe</c> (no ServiceController assembly dependency).</summary>
[SupportedOSPlatform("windows")]
public sealed class WindowsTlsServiceController : ITlsServiceController
{
    public string? Status(string serviceName)
    {
        var (exit, stdout, _) = Run("sc", $"query \"{serviceName}\"");
        if (exit != 0)
            return null;
        if (stdout.Contains("RUNNING", StringComparison.OrdinalIgnoreCase))
            return "Running";
        if (stdout.Contains("STOPPED", StringComparison.OrdinalIgnoreCase))
            return "Stopped";
        if (stdout.Contains("START_PENDING", StringComparison.OrdinalIgnoreCase))
            return "StartPending";
        if (stdout.Contains("STOP_PENDING", StringComparison.OrdinalIgnoreCase))
            return "StopPending";
        return "Unknown";
    }

    public void Stop(string serviceName)
    {
        var status = Status(serviceName);
        if (string.Equals(status, "Stopped", StringComparison.OrdinalIgnoreCase))
            return;
        Run("sc", $"stop \"{serviceName}\"");
        var deadline = DateTime.UtcNow.AddSeconds(60);
        while (DateTime.UtcNow < deadline)
        {
            if (string.Equals(Status(serviceName), "Stopped", StringComparison.OrdinalIgnoreCase))
                return;
            Thread.Sleep(500);
        }

        throw new TimeoutException("Timed out waiting for service stop: " + serviceName);
    }

    public void Start(string serviceName)
    {
        var status = Status(serviceName);
        if (string.Equals(status, "Running", StringComparison.OrdinalIgnoreCase))
            return;
        Run("sc", $"start \"{serviceName}\"");
        var deadline = DateTime.UtcNow.AddSeconds(60);
        while (DateTime.UtcNow < deadline)
        {
            if (string.Equals(Status(serviceName), "Running", StringComparison.OrdinalIgnoreCase))
                return;
            Thread.Sleep(500);
        }

        throw new TimeoutException("Timed out waiting for service start: " + serviceName);
    }

    private static (int Exit, string StdOut, string StdErr) Run(string file, string args)
    {
        var psi = new ProcessStartInfo
        {
            FileName = file,
            Arguments = args,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };
        using var p = Process.Start(psi) ?? throw new InvalidOperationException("Failed to start " + file);
        var stdout = p.StandardOutput.ReadToEnd();
        var stderr = p.StandardError.ReadToEnd();
        p.WaitForExit(60_000);
        return (p.ExitCode, stdout, stderr);
    }
}

public sealed class RecordingTlsServiceController : ITlsServiceController
{
    public List<string> Actions { get; } = new();
    public bool FailStart { get; set; }
    public string CurrentStatus { get; set; } = "Running";

    public string? Status(string serviceName) => CurrentStatus;

    public void Stop(string serviceName)
    {
        Actions.Add("stop:" + serviceName);
        CurrentStatus = "Stopped";
    }

    public void Start(string serviceName)
    {
        Actions.Add("start:" + serviceName);
        if (FailStart)
            throw new InvalidOperationException("simulated start failure");
        CurrentStatus = "Running";
    }
}
