using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
using System.Text.RegularExpressions;
using Microsoft.AspNetCore.DataProtection.KeyManagement;
using Microsoft.AspNetCore.DataProtection.Repositories;
using Microsoft.Data.SqlClient;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// P0/P1 hardening: TLS validation modes, SQL TrustServerCertificate policy, Data Protection persistence, installer gates.
/// </summary>
public sealed class TlsSqlAndDataProtectionHardeningTests
{
    [Fact]
    public void TestStoreCertificateProbeDoesNotOverrideSystemValidation()
    {
        var store = new CertificateOptions
        {
            Mode = CertificateMode.Store,
            Thumbprint = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            ValidationMode = CertificateValidationMode.SystemTrust
        };
        Assert.Equal(CertificateValidationMode.SystemTrust, LocalTlsHealth.ResolveValidationMode(store));

        // Thumbprint present must NOT install a custom callback for SystemTrust.
        var ssl = LocalTlsHealth.CreateProbeSslOptions(
            "control.example.com",
            CertificateValidationMode.SystemTrust,
            store.Thumbprint);
        Assert.Equal("control.example.com", ssl.TargetHost);
        Assert.Null(ssl.RemoteCertificateValidationCallback);

        // Auto-resolve: Store without ValidationMode → SystemTrust
        Assert.Equal(
            CertificateValidationMode.SystemTrust,
            LocalTlsHealth.ResolveValidationMode(new CertificateOptions { Mode = CertificateMode.Store }));
    }

    [Fact]
    public void TestStoreCertificateWrongHostnameRejected()
    {
        using var cert = CertificateLoader.CreateSelfSigned("correct.nyxveil.local");
        Assert.False(cert.MatchesHostname("wrong.nyxveil.local"));
        Assert.False(LocalTlsHealth.ValidateCertificateForHostname(cert, "wrong.nyxveil.local"));
    }

    [Fact]
    public void TestStoreCertificateHostnameMatchAccepted()
    {
        using var cert = CertificateLoader.CreateSelfSigned("match.nyxveil.local");
        Assert.True(cert.MatchesHostname("match.nyxveil.local"));
        Assert.True(LocalTlsHealth.ValidateCertificateForHostname(cert, "match.nyxveil.local"));
    }

    [Fact]
    public void TestSelfSignedPinnedCorrectHostnameAccepted()
    {
        using var cert = CertificateLoader.CreateSelfSigned("pin-ok.nyxveil.local");
        Assert.True(CertificateHostnameValidator.ValidatePinnedCertificate(
            cert, "pin-ok.nyxveil.local", cert.Thumbprint));

        var ssl = LocalTlsHealth.CreateProbeSslOptions(
            "pin-ok.nyxveil.local",
            CertificateValidationMode.SelfSignedPinned,
            cert.Thumbprint);
        Assert.NotNull(ssl.RemoteCertificateValidationCallback);
        Assert.True(ssl.RemoteCertificateValidationCallback!(
            null!, cert, null, SslPolicyErrors.RemoteCertificateChainErrors));
    }

    [Fact]
    public void TestSelfSignedPinnedWrongHostnameRejected()
    {
        using var cert = CertificateLoader.CreateSelfSigned("pin-host.nyxveil.local");
        Assert.False(CertificateHostnameValidator.ValidatePinnedCertificate(
            cert, "other.nyxveil.local", cert.Thumbprint));

        var ssl = LocalTlsHealth.CreateProbeSslOptions(
            "other.nyxveil.local",
            CertificateValidationMode.SelfSignedPinned,
            cert.Thumbprint);
        Assert.False(ssl.RemoteCertificateValidationCallback!(
            null!, cert, null, SslPolicyErrors.RemoteCertificateNameMismatch));
    }

    [Fact]
    public void TestSelfSignedPinnedWrongThumbprintRejected()
    {
        using var cert = CertificateLoader.CreateSelfSigned("pin-thumb.nyxveil.local");
        const string wrong = "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF";
        Assert.False(CertificateHostnameValidator.ValidatePinnedCertificate(
            cert, "pin-thumb.nyxveil.local", wrong));

        var ssl = LocalTlsHealth.CreateProbeSslOptions(
            "pin-thumb.nyxveil.local",
            CertificateValidationMode.SelfSignedPinned,
            wrong);
        Assert.False(ssl.RemoteCertificateValidationCallback!(
            null!, cert, null, SslPolicyErrors.None));
    }

    [Fact]
    public void TestInstallerRejectsCertificateForWrongPublicHostname()
    {
        using var cert = CertificateLoader.CreateSelfSigned("public.nyxveil.local");
        Assert.True(cert.MatchesHostname("public.nyxveil.local"));
        Assert.False(cert.MatchesHostname("attacker.example.com"));
        Assert.False(cert.MatchesHostname("nyxveil.local")); // not a parent match
    }

    [Fact]
    public void TestRemoteSqlDefaultsToTrustServerCertificateFalse()
    {
        var applied = DatabaseConnectionStringProvider.ApplyTlsPolicy(
            "Server=sql.remote.example;Database=NyxveilControlPlane;Trusted_Connection=True;TrustServerCertificate=True;Encrypt=True",
            new DatabaseOptions());
        var builder = new SqlConnectionStringBuilder(applied);
        Assert.False(builder.TrustServerCertificate);
        Assert.True(builder.Encrypt);
    }

    [Fact]
    public void TestLocalSqlCanUseConfiguredTrustPolicy()
    {
        var applied = DatabaseConnectionStringProvider.ApplyTlsPolicy(
            "Server=localhost;Database=NyxveilControlPlane;Trusted_Connection=True;Encrypt=True",
            new DatabaseOptions { TrustSqlServerCertificate = true, Encrypt = true });
        var builder = new SqlConnectionStringBuilder(applied);
        Assert.True(builder.TrustServerCertificate);
        Assert.True(builder.Encrypt);
    }

    [Fact]
    public void TestRemoteSqlTrustOverrideRequiresExplicitSetting()
    {
        // CS says Trust=true, but without Database:TrustSqlServerCertificate the provider forces false.
        var config = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ConnectionStrings:ControlPlane"] =
                    "Server=sql.contoso.local;Database=NyxveilControlPlane;User ID=nvp;Password=x;TrustServerCertificate=True;Encrypt=True"
            })
            .Build();

        var provider = new DatabaseConnectionStringProvider(config);
        var builder = new SqlConnectionStringBuilder(provider.GetConnectionString());
        Assert.False(builder.TrustServerCertificate);

        var explicitConfig = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ConnectionStrings:ControlPlane"] =
                    "Server=sql.contoso.local;Database=NyxveilControlPlane;User ID=nvp;Password=x;Encrypt=True",
                ["Database:TrustSqlServerCertificate"] = "true"
            })
            .Build();
        var trusted = new SqlConnectionStringBuilder(
            new DatabaseConnectionStringProvider(explicitConfig).GetConnectionString());
        Assert.True(trusted.TrustServerCertificate);
    }

    [Fact]
    public void TestDataProtectionKeysUsePersistentProductionRepository()
    {
        var keysDir = ServiceCollectionExtensions.GetDataProtectionKeysDirectory();
        Assert.Contains("data-protection", keysDir, StringComparison.OrdinalIgnoreCase);
        Assert.Contains(Path.Combine("Nyxveil", "ControlPlane"), keysDir, StringComparison.OrdinalIgnoreCase);

        var config = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ASPNETCORE_ENVIRONMENT"] = "Production",
                ["ConnectionStrings:ControlPlane"] =
                    "Server=(localdb)\\mssqllocaldb;Database=NyxveilControlPlane_DpTest;Trusted_Connection=True;Encrypt=True"
            })
            .Build();

        if (!OperatingSystem.IsWindows())
        {
            Assert.False(ServiceCollectionExtensions.ShouldPersistDataProtectionKeys(config));
            return;
        }

        Assert.True(ServiceCollectionExtensions.ShouldPersistDataProtectionKeys(config));

        var services = new ServiceCollection();
        services.AddLogging();
        services.AddSingleton<IConfiguration>(config);
        services.AddInfrastructure(config);

        using var sp = services.BuildServiceProvider();
        var km = sp.GetRequiredService<IOptions<KeyManagementOptions>>().Value;
        Assert.NotNull(km.XmlRepository);
        var fs = Assert.IsType<FileSystemXmlRepository>(km.XmlRepository);
        Assert.Equal(
            new DirectoryInfo(keysDir).FullName,
            fs.Directory.FullName,
            ignoreCase: true);
    }

    [Fact]
    public void TestInstallerDoesNotSetMachineWideAspNetCoreEnvironment()
    {
        var install = File.ReadAllText(PreDeployGateTests.FindLicensingFile("scripts", "install-windows.ps1"));
        var deploy = File.ReadAllText(PreDeployGateTests.FindLicensingFile("scripts", "Nyxveil.ControlPlane.Deploy.psm1"));

        Assert.DoesNotMatch(
            new Regex(@"SetEnvironmentVariable\s*\(\s*['""]ASPNETCORE_ENVIRONMENT['""].*Machine",
                RegexOptions.IgnoreCase | RegexOptions.Singleline),
            install);
        Assert.DoesNotMatch(
            new Regex(@"SetEnvironmentVariable\s*\(\s*['""]ASPNETCORE_ENVIRONMENT['""].*Machine",
                RegexOptions.IgnoreCase | RegexOptions.Singleline),
            deploy);
        Assert.DoesNotContain("[Environment]::SetEnvironmentVariable('ASPNETCORE_ENVIRONMENT', 'Production', 'Machine')",
            install, StringComparison.Ordinal);
        Assert.DoesNotContain("[Environment]::SetEnvironmentVariable(\"ASPNETCORE_ENVIRONMENT\", \"Production\", \"Machine\")",
            install, StringComparison.Ordinal);
    }

    [Fact]
    public void TestServiceCreatedBeforeServiceSidPermissionsGranted()
    {
        var install = File.ReadAllText(PreDeployGateTests.FindLicensingFile("scripts", "install-windows.ps1"));

        var serviceIdx = install.IndexOf("New-NyxveilWindowsService", StringComparison.Ordinal);
        var aclIdx = install.IndexOf("Set-NyxveilDirectoryAcls", StringComparison.Ordinal);
        var certAclIdx = install.IndexOf("Grant-CertificatePrivateKeyAccess", StringComparison.Ordinal);
        var sqlGrantIdx = install.LastIndexOf("Grant-SqlLoginForServiceAccount", StringComparison.Ordinal);

        Assert.True(serviceIdx >= 0, "New-NyxveilWindowsService missing");
        Assert.True(aclIdx >= 0, "Set-NyxveilDirectoryAcls missing");
        Assert.True(certAclIdx >= 0, "Grant-CertificatePrivateKeyAccess missing");
        Assert.True(serviceIdx < aclIdx,
            "Service must be created before Set-NyxveilDirectoryAcls (ServiceSid).");
        Assert.True(serviceIdx < certAclIdx,
            "Service must be created before Grant-CertificatePrivateKeyAccess (ServiceSid).");
        if (sqlGrantIdx >= 0)
            Assert.True(serviceIdx < sqlGrantIdx,
                "Service must be created before Grant-SqlLoginForServiceAccount (ServiceSid).");
    }
}
