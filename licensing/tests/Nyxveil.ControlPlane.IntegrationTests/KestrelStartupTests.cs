using System.Net;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Web.Hosting;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class KestrelStartupTests
{
    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task HostingListenerStartsRealHttpsWithoutLegacyOrPhantomEndpoints(bool legacyEndpoints)
    {
        using var generated = CertificateLoader.CreateSelfSigned("localhost");
        // Schannel requires a key container. Without PersistKeySet this temporary
        // user-key container is removed when the certificate is disposed.
        using var certificate = X509CertificateLoader.LoadPkcs12(generated.Export(X509ContentType.Pfx), null,
            X509KeyStorageFlags.UserKeySet | X509KeyStorageFlags.Exportable);
        var builder = WebApplication.CreateBuilder(new WebApplicationOptions { EnvironmentName = "Production" });
        builder.Configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Kestrel:EndpointDefaults:Protocols"] = "Http1"
        });
        if (legacyEndpoints)
        {
            // Exactly the null Http/HTTPS sections that used to crash UseHttps,
            // plus a legacy custom name that must not create a second listener.
            builder.Configuration.AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Kestrel:Endpoints:Http:Url"] = null,
                ["Kestrel:Endpoints:Https:Certificate:Thumbprint"] = null,
                ["Kestrel:Endpoints:OldPanel:Url"] = "http://127.0.0.1:1"
            });
        }
        KestrelEndpointConfiguration.Configure(builder.WebHost, builder.Configuration.GetSection("Kestrel"),
            IPAddress.Loopback, 0, certificate);
        await using var app = builder.Build();
        app.MapGet("/health/live", () => "healthy");
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(20));
        await app.StartAsync(timeout.Token);
        try
        {
            var address = Assert.Single(app.Urls);
            Assert.StartsWith("https://", address, StringComparison.Ordinal);
            using var handler = new HttpClientHandler
            {
                ServerCertificateCustomValidationCallback = (_, presented, _, errors) =>
                    presented is not null && presented.RawData.AsSpan().SequenceEqual(certificate.RawData) &&
                    (errors & ~SslPolicyErrors.RemoteCertificateChainErrors) == SslPolicyErrors.None
            };
            using var client = new HttpClient(handler)
            {
                DefaultRequestVersion = HttpVersion.Version20,
                DefaultVersionPolicy = HttpVersionPolicy.RequestVersionOrLower
            };
            var url = address.Replace("127.0.0.1", "localhost", StringComparison.Ordinal) + "/health/live";
            using var response = await client.GetAsync(url, timeout.Token);
            response.EnsureSuccessStatusCode();
            Assert.Equal(HttpVersion.Version11, response.Version);
            Assert.Equal("healthy", await response.Content.ReadAsStringAsync(timeout.Token));
            // A later config update must not reintroduce the ignored listeners.
            builder.Configuration["Kestrel:Endpoints:Http:Url"] = null;
            ((IConfigurationRoot)builder.Configuration).Reload();
            Assert.Equal("healthy", await client.GetStringAsync(url, timeout.Token));
            Assert.Single(app.Urls);
        }
        finally
        {
            await app.StopAsync(CancellationToken.None);
        }
    }
}
