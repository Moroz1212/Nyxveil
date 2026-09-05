using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Web.Cli;

/// <summary>Thin host self-test used by installer scripts (DB + signing key + optional TLS probe).</summary>
public static class SelfTestCommand
{
    public static async Task<int> RunAsync(
        string[] args,
        bool probeTls = false,
        CancellationToken cancellationToken = default)
    {
        try
        {
            var builder = Host.CreateApplicationBuilder(args);
            builder.Configuration["Https:RequireHttpsInProduction"] = "false";

            var programData = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
                "Nyxveil",
                "ControlPlane");
            if (Directory.Exists(programData))
                builder.Configuration.AddNyxveilProtectedSecrets(programData);

            builder.Services.AddApplication(builder.Configuration);
            builder.Services.AddInfrastructure(builder.Configuration);

            using var host = builder.Build();
            using var scope = host.Services.CreateScope();

            var csProvider = scope.ServiceProvider.GetRequiredService<IDatabaseConnectionStringProvider>();
            var cs = csProvider.GetConnectionString();
            if (string.IsNullOrWhiteSpace(cs))
            {
                Console.Error.WriteLine("FAIL: connection string missing");
                return 1;
            }

            // Never log the connection string (may contain SQL password overlay).
            Console.WriteLine("OK: connection string resolved (" +
                              DatabaseConnectionStringProvider.DescribeWithoutPassword(cs) + ")");

            var keys = scope.ServiceProvider.GetRequiredService<ISigningKeyService>();
            var material = await keys.GetCurrentSigningMaterialAsync(cancellationToken).ConfigureAwait(false);
            if (string.IsNullOrWhiteSpace(material.KeyId) || material.PublicKey.Length == 0)
            {
                Console.Error.WriteLine("FAIL: signing key unavailable");
                return 1;
            }

            var config = scope.ServiceProvider.GetRequiredService<IConfiguration>();
            var certOptions = CertificateLoader.Bind(config);
            var hosting = config.GetSection(HostingOptions.SectionName).Get<HostingOptions>() ?? new HostingOptions();
            if (!string.IsNullOrWhiteSpace(certOptions.Thumbprint) ||
                !string.IsNullOrWhiteSpace(certOptions.PfxPath) ||
                certOptions.Mode == CertificateMode.SelfSigned)
            {
                if (!CertificateLoader.TryLoad(certOptions, hosting.PublicHostname, out var cert, out var error))
                {
                    Console.Error.WriteLine("WARN: certificate not loadable: " + error);
                }
                else
                {
                    cert?.Dispose();
                    Console.WriteLine("OK: certificate loadable");
                }
            }

            if (probeTls || args.Any(a => a.Equals("self-test-http", StringComparison.OrdinalIgnoreCase) ||
                                          a.Equals("--tls", StringComparison.OrdinalIgnoreCase)))
            {
                var (ok, message) = await LocalTlsHealth.ProbeAsync(config, cancellationToken).ConfigureAwait(false);
                if (!ok)
                {
                    Console.Error.WriteLine("FAIL: " + message);
                    return 1;
                }

                Console.WriteLine("OK: " + message);
            }

            Console.WriteLine("OK: self-test passed (signing key " + material.KeyId + ")");
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine("FAIL: " + ex.Message);
            return 1;
        }
    }
}
