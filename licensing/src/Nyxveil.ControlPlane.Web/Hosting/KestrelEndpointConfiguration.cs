using System.Net;
using System.Security.Cryptography.X509Certificates;

namespace Nyxveil.ControlPlane.Web.Hosting;

public static class KestrelEndpointConfiguration
{
    /// <summary>
    /// Hosting supplies the single HTTPS listener. Replace the default Kestrel
    /// loader before UseHttps can load stale endpoints. Null configuration values
    /// do not remove sections: they leave endpoints with a missing Url.
    /// Preserve the other loader settings, including protocol and TLS defaults.
    /// </summary>
    public static void Configure(IWebHostBuilder builder, IConfigurationSection kestrel,
        IPAddress address, int port, X509Certificate2 certificate)
    {
        var settings = new ConfigurationBuilder().AddInMemoryCollection(
            kestrel.AsEnumerable(makePathsRelative: true).Where(pair =>
                !pair.Key.Equals("Endpoints", StringComparison.OrdinalIgnoreCase) &&
                !pair.Key.StartsWith("Endpoints:", StringComparison.OrdinalIgnoreCase))).Build();

        builder.ConfigureKestrel(options =>
        {
            options.Configure(settings, reloadOnChange: false);
            options.Listen(address, port, listen => listen.UseHttps(certificate));
        });
    }
}
