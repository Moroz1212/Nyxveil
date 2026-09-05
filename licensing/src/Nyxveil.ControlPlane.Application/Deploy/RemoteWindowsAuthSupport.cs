namespace Nyxveil.ControlPlane.Application.Deploy;

/// <summary>
/// C# mirror of Deploy.psm1 Test-RemoteWindowsAuthSupported.
/// Auth matrix:
/// LOCAL + NT SERVICE\* — OK;
/// REMOTE + Sql — OK;
/// REMOTE + gMSA (account ends with $) — OK if resolvable;
/// REMOTE + ordinary domain user/password — NOT SUPPORTED;
/// REMOTE + NT SERVICE\* — NOT SUPPORTED.
/// </summary>
public static class RemoteWindowsAuthSupport
{
    public static bool IsLocalDatabaseServer(string? databaseServer)
    {
        if (string.IsNullOrWhiteSpace(databaseServer))
            return true;

        var s = databaseServer.Trim();
        var hostPart = s.Split(['\\', ','], 2)[0].Trim();
        if (hostPart is "localhost" or "127.0.0.1" or "." or "::1")
            return true;

        var computer = Environment.MachineName;
        if (string.Equals(hostPart, computer, StringComparison.OrdinalIgnoreCase))
            return true;

        try
        {
            if (string.Equals(hostPart, System.Net.Dns.GetHostName(), StringComparison.OrdinalIgnoreCase))
                return true;
        }
        catch
        {
            // ignore DNS failures — treat as remote
        }

        return false;
    }

    /// <summary>
    /// Returns true when the combination is supported; throws when unsupported.
    /// </summary>
    public static bool EnsureSupported(string databaseServer, string databaseAuth, string serviceAccount)
    {
        if (!string.Equals(databaseAuth, "Windows", StringComparison.OrdinalIgnoreCase))
            return true;

        if (IsLocalDatabaseServer(databaseServer))
            return true;

        var acct = (serviceAccount ?? string.Empty).Trim();
        if (acct.StartsWith("NT SERVICE\\", StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidOperationException(
                $"Remote SQL Server '{databaseServer}' with Windows Auth is not supported for virtual service account '{serviceAccount}'. " +
                "NT SERVICE\\* identities are local-only and cannot authenticate to a remote SQL host. " +
                "Choose DatabaseAuth=Sql, a gMSA (account ending with $), or install SQL Server locally.");
        }

        if (acct.EndsWith('$'))
        {
            if (!OperatingSystem.IsWindows())
            {
                throw new InvalidOperationException(
                    $"Remote Windows Auth gMSA '{serviceAccount}' can only be resolved on Windows.");
            }

            try
            {
                var nt = new System.Security.Principal.NTAccount(acct);
                _ = nt.Translate(typeof(System.Security.Principal.SecurityIdentifier));
                return true;
            }
            catch (Exception ex) when (ex is not InvalidOperationException)
            {
                throw new InvalidOperationException(
                    $"Remote Windows Auth gMSA '{serviceAccount}' could not be resolved on this machine. " +
                    "Ensure the gMSA is installed/usable here (no password parameter is accepted). " + ex.Message,
                    ex);
            }
        }

        throw new InvalidOperationException(
            $"Remote SQL Server '{databaseServer}' with Windows Auth is not supported for ordinary domain account '{serviceAccount}'. " +
            "Installer does not accept a domain user password for the service identity. " +
            "Supported: LOCAL + NT SERVICE\\*, REMOTE + Sql, or REMOTE + gMSA (name ending with $).");
    }
}
