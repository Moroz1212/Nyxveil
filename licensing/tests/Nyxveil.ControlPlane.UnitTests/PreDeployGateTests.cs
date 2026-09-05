using System.IO.Compression;
using System.Text.RegularExpressions;
using Nyxveil.ControlPlane.Application.Deploy;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Hosting;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Mandatory pre-deploy regression gates (installer / Deploy.psm1 / pack / TLS hostname).
/// Certificate persistence, KEK recovery, and production cert fail-closed live in HttpsAndCertificateTests.
/// </summary>
public sealed class PreDeployGateTests
{
    [Fact]
    public void TestHealthUsesCertificateHostname()
    {
        Assert.Equal(
            "cp.example.com",
            LocalTlsHealth.ResolveProbeHostname(new HostingOptions { PublicHostname = "cp.example.com" }));
        Assert.Equal(
            "localhost",
            LocalTlsHealth.ResolveProbeHostname(new HostingOptions { PublicHostname = "  " }));
        Assert.Equal(
            "cp.example.com",
            LocalTlsHealth.ResolveProbeHostname(new HostingOptions { PublicHostname = "https://cp.example.com:8443/" }));

        var deploy = File.ReadAllText(FindLicensingFile("scripts", "Nyxveil.ControlPlane.Deploy.psm1"));
        Assert.Contains("function Get-HealthTarget", deploy, StringComparison.Ordinal);
        Assert.Contains("PublicHostname", deploy, StringComparison.Ordinal);
        Assert.Contains("$url = \"https://${hostName}:${Port}\"", deploy, StringComparison.Ordinal);
    }

    [Fact]
    public void TestLocalTlsHealthValidatesExpectedHostname()
    {
        using var matching = CertificateLoader.CreateSelfSigned("gate-match.nyxveil.local");
        Assert.True(LocalTlsHealth.ValidateCertificateForHostname(matching, "gate-match.nyxveil.local"));
        Assert.False(LocalTlsHealth.ValidateCertificateForHostname(matching, "wrong.nyxveil.local"));

        using var other = CertificateLoader.CreateSelfSigned("other.nyxveil.local");
        Assert.True(LocalTlsHealth.ValidateCertificateForHostname(other, "other.nyxveil.local"));
        Assert.False(LocalTlsHealth.ValidateCertificateForHostname(other, "gate-match.nyxveil.local"));

        // CreateSelfSigned sets CN + DNS SAN; MatchesHostname is the acceptance gate.
        Assert.Contains("CN=gate-match.nyxveil.local", matching.Subject, StringComparison.OrdinalIgnoreCase);
        Assert.True(matching.MatchesHostname("gate-match.nyxveil.local"));
    }

    [Fact]
    public void TestSqlAuthSelfTestUsesSqlAuthentication()
    {
        var deploy = File.ReadAllText(FindLicensingFile("scripts", "Nyxveil.ControlPlane.Deploy.psm1"));
        Assert.Contains("SQLCMDPASSWORD", deploy, StringComparison.Ordinal);
        Assert.Contains("if ($DatabaseAuth -eq 'Sql')", deploy, StringComparison.Ordinal);
        Assert.Contains("[void]$sqlArgs.Add('-E')", deploy, StringComparison.Ordinal);

        // Sql path must not unconditionally add -E; Windows branch uses -E after the Sql if.
        var sqlBranch = Regex.Match(
            deploy,
            @"if \(\$DatabaseAuth -eq 'Sql'\)\s*\{(?<sql>.*?)\}\s*else\s*\{(?<win>.*?)\}\s*if \(\$InputFile\)",
            RegexOptions.Singleline);
        Assert.True(sqlBranch.Success, "Get-NyxveilSqlcmdArgs must branch Sql vs Windows auth.");
        Assert.Contains("-U", sqlBranch.Groups["sql"].Value, StringComparison.Ordinal);
        Assert.DoesNotContain("Add('-E')", sqlBranch.Groups["sql"].Value, StringComparison.Ordinal);
        Assert.Contains("Add('-E')", sqlBranch.Groups["win"].Value, StringComparison.Ordinal);

        var selfTest = File.ReadAllText(FindLicensingFile("scripts", "self-test.ps1"));
        Assert.Contains("SQLCMDPASSWORD", selfTest, StringComparison.Ordinal);
        Assert.Contains("$dbAuth -eq 'Sql'", selfTest, StringComparison.Ordinal);
        Assert.Contains("Invoke-NyxveilSql", selfTest, StringComparison.Ordinal);
        Assert.Contains("-DatabaseAuth $dbAuth", selfTest, StringComparison.Ordinal);
    }

    [Fact]
    public void TestUpdateSupportsSqlAuthentication()
    {
        var update = File.ReadAllText(FindLicensingFile("scripts", "update-windows.ps1"));
        Assert.Contains("Invoke-NyxveilSql", update, StringComparison.Ordinal);
        Assert.Contains("DatabaseAuth", update, StringComparison.Ordinal);
        Assert.Contains("-DatabaseAuth $dbAuth", update, StringComparison.Ordinal);
    }

    [Fact]
    public void TestRemoteWindowsAuthRequiresSupportedServiceIdentity()
    {
        Assert.True(RemoteWindowsAuthSupport.EnsureSupported(
            "localhost", "Windows", @"NT SERVICE\NyxveilControlPlane"));
        Assert.True(RemoteWindowsAuthSupport.EnsureSupported(
            "sql.contoso.local", "Sql", @"NT SERVICE\NyxveilControlPlane"));

        var ntServiceRemote = Assert.Throws<InvalidOperationException>(() =>
            RemoteWindowsAuthSupport.EnsureSupported(
                "sql.contoso.local", "Windows", @"NT SERVICE\NyxveilControlPlane"));
        Assert.Contains("NT SERVICE", ntServiceRemote.Message, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("remote", ntServiceRemote.Message, StringComparison.OrdinalIgnoreCase);

        var ordinaryDomain = Assert.Throws<InvalidOperationException>(() =>
            RemoteWindowsAuthSupport.EnsureSupported(
                "sql.contoso.local", "Windows", @"CONTOSO\svc-nyxveil"));
        Assert.Contains("ordinary domain", ordinaryDomain.Message, StringComparison.OrdinalIgnoreCase);

        var deploy = File.ReadAllText(FindLicensingFile("scripts", "Nyxveil.ControlPlane.Deploy.psm1"));
        Assert.Contains("function Test-RemoteWindowsAuthSupported", deploy, StringComparison.Ordinal);
        Assert.Contains("NT SERVICE\\*", deploy, StringComparison.Ordinal);
        Assert.Contains("gMSA", deploy, StringComparison.Ordinal);
    }

    [Fact]
    public void TestInstallerDoesNotSetMachineWideAspNetCoreEnvironment()
    {
        var install = File.ReadAllText(FindLicensingFile("scripts", "install-windows.ps1"));
        var deploy = File.ReadAllText(FindLicensingFile("scripts", "Nyxveil.ControlPlane.Deploy.psm1"));
        Assert.DoesNotContain("SetEnvironmentVariable('ASPNETCORE_ENVIRONMENT'", install, StringComparison.Ordinal);
        Assert.DoesNotContain("SetEnvironmentVariable(\"ASPNETCORE_ENVIRONMENT\"", install, StringComparison.Ordinal);
        Assert.DoesNotContain("SetEnvironmentVariable('DOTNET_ENVIRONMENT'", install, StringComparison.Ordinal);
        Assert.DoesNotContain(", 'Machine')", install, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain(", \"Machine\")", install, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("Set-NyxveilServiceEnvironment", install, StringComparison.Ordinal);
        Assert.Contains("function Set-NyxveilServiceEnvironment", deploy, StringComparison.Ordinal);
        Assert.Contains("ASPNETCORE_ENVIRONMENT=Production", deploy, StringComparison.Ordinal);
    }

    [Fact]
    public void TestServiceCreatedBeforeServiceSidPermissionsGranted()
    {
        var install = File.ReadAllText(FindLicensingFile("scripts", "install-windows.ps1"));
        var createIdx = install.IndexOf("New-NyxveilWindowsService", StringComparison.Ordinal);
        var sidIdx = install.IndexOf("Ensure-NyxveilServiceSid", StringComparison.Ordinal);
        var envIdx = install.IndexOf("Set-NyxveilServiceEnvironment", StringComparison.Ordinal);
        var aclIdx = install.IndexOf("Set-NyxveilDirectoryAcls", StringComparison.Ordinal);
        var certIdx = install.IndexOf("Grant-CertificatePrivateKeyAccess", StringComparison.Ordinal);
        var sqlIdx = install.IndexOf("Grant-SqlLoginForServiceAccount", StringComparison.Ordinal);
        var startIdx = install.IndexOf("Start-Service -Name $ServiceName", StringComparison.Ordinal);

        Assert.True(createIdx >= 0, "New-NyxveilWindowsService missing");
        Assert.True(sidIdx > createIdx, "Ensure-NyxveilServiceSid must follow service create");
        Assert.True(envIdx > sidIdx, "Set-NyxveilServiceEnvironment must follow SID");
        Assert.True(aclIdx > envIdx, "Set-NyxveilDirectoryAcls must follow service Environment");
        Assert.True(certIdx > aclIdx, "Grant-CertificatePrivateKeyAccess must follow ACLs");
        Assert.True(sqlIdx > certIdx, "Grant-SqlLoginForServiceAccount must follow cert ACL / SID");
        Assert.True(startIdx > sqlIdx, "Start-Service must be last among permission steps");
    }

    [Fact]
    public void TestFreshInstallDoesNotOverwriteExistingKek()
    {
        var install = File.ReadAllText(FindLicensingFile("scripts", "install-windows.ps1"));
        Assert.Contains("Initialize-NyxveilRestrictedDirectory", install, StringComparison.Ordinal);
        Assert.Contains("license-kek.dpapi", install, StringComparison.Ordinal);
        Assert.Contains("Preserving existing license-kek.dpapi", install, StringComparison.Ordinal);
        Assert.Contains("NEVER overwrite", install, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("restore-recovery.ps1", install, StringComparison.Ordinal);
        // Fresh must not regenerate when file exists (all modes preserve).
        Assert.DoesNotContain("$preserveKek = ($InstallMode -eq 'Repair'", install, StringComparison.Ordinal);
    }

    [Fact]
    public void TestExistingInstallDoesNotRotateKek()
    {
        var install = File.ReadAllText(FindLicensingFile("scripts", "install-windows.ps1"));
        Assert.Contains("license-kek.dpapi", install, StringComparison.Ordinal);
        Assert.Contains("Preserving existing license-kek.dpapi", install, StringComparison.Ordinal);
        Assert.Contains("Test-Path -LiteralPath $kekPath", install, StringComparison.Ordinal);
        Assert.Contains("Repair", install, StringComparison.Ordinal);
        Assert.Contains("Upgrade", install, StringComparison.Ordinal);
    }

    [Fact]
    public void TestZipUsesForwardSlashPathsOnly()
    {
        var pack = File.ReadAllText(FindLicensingFile("scripts", "pack-release.ps1"));
        Assert.Contains("-replace '\\\\', '/'", pack, StringComparison.Ordinal);

        var tempRoot = Path.Combine(Path.GetTempPath(), "nyxveil-zip-gate-" + Guid.NewGuid().ToString("N"));
        var zipPath = tempRoot + ".zip";
        Directory.CreateDirectory(Path.Combine(tempRoot, "subdir"));
        File.WriteAllText(Path.Combine(tempRoot, "subdir", "hello.txt"), "ok");

        try
        {
            using (var fs = File.Create(zipPath))
            using (var archive = new ZipArchive(fs, ZipArchiveMode.Create))
            {
                foreach (var file in Directory.EnumerateFiles(tempRoot, "*", SearchOption.AllDirectories))
                {
                    var rel = Path.GetRelativePath(tempRoot, file);
                    var entryName = rel.Replace('\\', '/');
                    archive.CreateEntryFromFile(file, entryName, CompressionLevel.Optimal);
                }
            }

            using var check = ZipFile.OpenRead(zipPath);
            Assert.NotEmpty(check.Entries);
            foreach (var e in check.Entries)
            {
                Assert.DoesNotContain('\\', e.FullName);
                Assert.Contains('/', e.FullName);
            }
        }
        finally
        {
            if (Directory.Exists(tempRoot))
                Directory.Delete(tempRoot, recursive: true);
            if (File.Exists(zipPath))
                File.Delete(zipPath);
        }
    }

    [Fact]
    public void TestPowerShellModuleImports()
    {
        var module = FindLicensingFile("scripts", "Nyxveil.ControlPlane.Deploy.psm1");
        Assert.True(File.Exists(module), module);

        var psi = new System.Diagnostics.ProcessStartInfo
        {
            FileName = ResolvePowerShellPath(),
            ArgumentList =
            {
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                $"Import-Module -LiteralPath '{module.Replace("'", "''")}' -Force -ErrorAction Stop; exit 0"
            },
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };

        using var proc = System.Diagnostics.Process.Start(psi)
                         ?? throw new InvalidOperationException("Failed to start PowerShell.");
        var stdout = proc.StandardOutput.ReadToEnd();
        var stderr = proc.StandardError.ReadToEnd();
        Assert.True(proc.WaitForExit(60_000), "PowerShell Import-Module timed out.");
        Assert.True(proc.ExitCode == 0,
            $"Import-Module failed (exit {proc.ExitCode}). stderr={stderr}\nstdout={stdout}");
    }

    [Theory]
    [InlineData("NyxveilControlPlane", true)]
    [InlineData("A", true)]
    [InlineData("_db", true)]
    [InlineData("db-1_x", true)]
    [InlineData("", false)]
    [InlineData("1bad", false)]
    [InlineData("has space", false)]
    [InlineData("bad;drop", false)]
    public void TestDatabaseNameValidation(string name, bool expected)
    {
        Assert.Equal(expected, DatabaseOptions.IsValidDatabaseName(name));
        Assert.Equal(expected, DatabaseOptions.DatabaseNamePattern.IsMatch(name));

        if (name.Length == 0)
            return;

        // Over-length: 129 chars after first char → invalid (max 128 total).
        if (expected)
        {
            var tooLong = "A" + new string('x', 127);
            Assert.True(DatabaseOptions.IsValidDatabaseName(tooLong));
            Assert.False(DatabaseOptions.IsValidDatabaseName(tooLong + "y"));
        }
    }

    private static string ResolvePowerShellPath()
    {
        var candidates = new[]
        {
            Path.Combine(Environment.SystemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe"),
            "powershell.exe",
            "pwsh.exe"
        };
        foreach (var c in candidates)
        {
            try
            {
                if (c.EndsWith(".exe", StringComparison.OrdinalIgnoreCase) && File.Exists(c))
                    return c;
            }
            catch
            {
                // continue
            }
        }

        return "powershell.exe";
    }

    internal static string FindLicensingRoot()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null)
        {
            var scripts = Path.Combine(dir.FullName, "scripts", "Nyxveil.ControlPlane.Deploy.psm1");
            if (File.Exists(scripts))
                return dir.FullName;

            var licensing = Path.Combine(dir.FullName, "licensing", "scripts", "Nyxveil.ControlPlane.Deploy.psm1");
            if (File.Exists(licensing))
                return Path.Combine(dir.FullName, "licensing");

            dir = dir.Parent;
        }

        throw new DirectoryNotFoundException("Could not locate licensing/ root from " + AppContext.BaseDirectory);
    }

    internal static string FindLicensingFile(params string[] relativeParts)
    {
        var parts = new string[relativeParts.Length + 1];
        parts[0] = FindLicensingRoot();
        relativeParts.CopyTo(parts, 1);
        return Path.Combine(parts);
    }
}
