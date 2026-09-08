using Microsoft.Extensions.Configuration;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class AcmeWizardServiceTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();

    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task Wizard_CreateOrder_VerifyDns_Finalize_Import_FakeProvider()
    {
        var config = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Hosting:PublicHostname"] = "cp.example.test",
                ["Hosting:PublicBaseUrl"] = "https://cp.example.test:18443",
                ["Certificate:Thumbprint"] = "OLDTHUMB"
            })
            .Build();

        var dns = new FakeDnsTxtLookup();
        var acme = new FakeAcmeDns01Provider();
        var status = new StubCpCertStatus();
        var wizard = new ControlPlaneAcmeWizardService(
            _fx.Db,
            _fx.Clock,
            acme,
            dns,
            status,
            config);

        var op = await wizard.StartAsync("sa@test");
        Assert.Equal(CertificateRenewalStatus.PendingDns, op.Status);
        Assert.Equal("cp.example.test", op.Domain);
        Assert.False(string.IsNullOrWhiteSpace(op.ChallengeValue));
        Assert.StartsWith("_acme-challenge.", op.ChallengeName);

        var miss = await wizard.VerifyDnsTxtAsync(op.Id);
        Assert.False(miss.Found);

        dns.Records[op.ChallengeName] = [op.ChallengeValue];
        var hit = await wizard.VerifyDnsTxtAsync(op.Id);
        Assert.True(hit.Found);

        op = await wizard.ContinueFinalizeAsync(op.Id);
        Assert.Equal(CertificateRenewalStatus.ReadyToImport, op.Status);
        Assert.False(string.IsNullOrWhiteSpace(op.NewThumbprint));

        op = await wizard.ImportAndSwitchAsync(op.Id);
        Assert.Equal(CertificateRenewalStatus.Completed, op.Status);
    }

    private sealed class FakeDnsTxtLookup : IDnsTxtLookup
    {
        public Dictionary<string, List<string>> Records { get; } = new(StringComparer.OrdinalIgnoreCase);

        public Task<IReadOnlyList<string>> LookupTxtAsync(string name, CancellationToken cancellationToken = default)
        {
            if (Records.TryGetValue(name, out var list))
                return Task.FromResult<IReadOnlyList<string>>(list);
            return Task.FromResult<IReadOnlyList<string>>(Array.Empty<string>());
        }
    }

    private sealed class StubCpCertStatus : IControlPlaneCertificateStatusService
    {
        public Task<ControlPlaneCertificateStatusDto> GetStatusAsync(CancellationToken cancellationToken = default) =>
            Task.FromResult(new ControlPlaneCertificateStatusDto
            {
                Hostname = "cp.example.test",
                Health = "Healthy",
                DaysRemaining = 60,
                Thumbprint = "OLDTHUMB",
                HasPrivateKey = true,
                SystemTrustOk = true
            });
    }
}
