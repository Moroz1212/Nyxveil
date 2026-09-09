using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;
using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class NodeReregisterCatalogTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();

    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    [Fact]
    public async Task TestExistingNodeRegistrationUpdatesServerVersion()
    {
        var (seed, _, req) = await RegisterAsync("node-ver");
        Assert.Equal("1.0.1", (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-ver")).ServerVersion);

        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerVersion = "1.0.10";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        Assert.Equal("1.0.10", (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-ver")).ServerVersion);
    }

    [Fact]
    public async Task TestExistingNodeRegistrationUpdatesServerName()
    {
        var (seed, _, req) = await RegisterAsync("node-sn");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerName = "fi-hel-01.nyxveil.ru";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        Assert.Equal("fi-hel-01.nyxveil.ru", (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-sn")).ServerName);
    }

    [Fact]
    public async Task TestExistingNodeRegistrationUpdatesSPKI()
    {
        var (seed, _, req) = await RegisterAsync("node-spki");
        var newPin = ControlPlaneTestFixture.RandomKey32();
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.SpkiPin = newPin;
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        Assert.Equal(newPin, (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-spki")).SpkiPin);
    }

    [Fact]
    public async Task TestExistingNodeRegistrationPreservesNodeId()
    {
        var (seed, _, req) = await RegisterAsync("node-keep-id");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerVersion = "9.9.9";
        var resp = await _fx.Nodes.RegisterWithBootstrapAsync(retry);
        Assert.Equal("node-keep-id", resp.NodeId);
        Assert.Equal(1, await _fx.Db.Nodes.CountAsync(n => n.NodeId == "node-keep-id"));
    }

    [Fact]
    public async Task TestExistingNodeRegistrationPreservesLocationId()
    {
        var (seed, _, req) = await RegisterAsync("node-keep-loc");
        var before = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-keep-loc");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerName = "fi-hel-01.nyxveil.ru";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);
        var after = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-keep-loc");
        Assert.Equal(before.LocationId, after.LocationId);
    }

    [Fact]
    public async Task TestExistingNodeCannotChangeAdminOwnedFields()
    {
        var (seed, _, req) = await RegisterAsync("node-admin");
        var before = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-admin");
        Assert.True(before.Enabled);
        Assert.False(before.TestOnly);

        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.TestOnly = true;
        retry.ServerVersion = "1.0.10";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var after = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-admin");
        Assert.True(after.Enabled);
        Assert.False(after.TestOnly);
        Assert.Equal(before.LocationId, after.LocationId);
        Assert.Equal(before.PublicIdentity, after.PublicIdentity);
        Assert.Equal("1.0.10", after.ServerVersion);
    }

    [Fact]
    public async Task TestExistingNodeCannotChangeLocationId()
    {
        var (seed, _, req) = await RegisterAsync("node-move");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.LocationId = _fx.LocationIdB;
        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
        Assert.Equal(_fx.LocationId, (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-move")).LocationId);
    }

    [Fact]
    public async Task TestHeartbeatUpdatesLiveHealthWithoutOverwritingRegistrationMetadata()
    {
        var (_, _, req) = await RegisterAsync("node-hb-meta");
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-hb-meta");
        var pin = node.SpkiPin!.ToArray();
        var ver = node.ServerVersion;
        var sn = node.ServerName;

        await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = "node-hb-meta",
            Version = "should-not-apply",
            ProtocolVersion = 99,
            Capacity = 50,
            CurrentSessions = 3,
            Healthy = true,
            CpuUsage = 1.5
        });

        var after = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-hb-meta");
        Assert.Equal(ver, after.ServerVersion);
        Assert.Equal(sn, after.ServerName);
        Assert.Equal(pin, after.SpkiPin);
        Assert.NotNull(after.LastSeenAt);
        Assert.Equal(3, after.CurrentSessions);
    }

    [Fact]
    public async Task TestCatalogUsesLatestNodeRegistrationMetadata()
    {
        var (seed, _, req) = await RegisterAsync("node-cat-meta");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerVersion = "1.0.10";
        retry.ServerName = "fi-hel-01.nyxveil.ru";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var token = await CreateUserLicenseTokenAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var node = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == "node-cat-meta");
        Assert.Equal("1.0.10", node.ServerVersion);
        Assert.Equal("fi-hel-01.nyxveil.ru", node.ServerName);
    }

    [Fact]
    public async Task TestCatalogUsesLatestSPKI()
    {
        var (seed, _, req) = await RegisterAsync("node-cat-spki");
        var pin = Convert.FromHexString("63855191aadfe5c14ac84483720625e45925d5423881aa3171f5c531576c4488");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.SpkiPin = pin;
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var token = await CreateUserLicenseTokenAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var node = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == "node-cat-spki");
        Assert.Equal(pin, node.SpkiPin);
        Assert.Equal(Convert.ToBase64String(pin), Convert.ToBase64String(node.SpkiPin!));
    }

    [Fact]
    public async Task TestSignedSelfUsesLatestSPKIAndValidSignature()
    {
        var (seed, _, req) = await RegisterAsync("node-self-spki");
        var pin = Convert.FromHexString("f3855191aadfe5c14ac84483720625e45925d5423881aa3171f5c531576c4488");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.SpkiPin = pin;
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var signed = await _fx.Catalog.GetSignedCatalogForNodeAsync("node-self-spki");
        Assert.Equal(_fx.LocationId, Assert.Single(signed.Catalog.Locations).LocationId);
        Assert.Equal(pin, Assert.Single(signed.Catalog.Nodes).SpkiPin);

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(signed.Catalog);
        var keys = await _fx.Scope.ServiceProvider.GetRequiredService<ISigningKeyService>()
            .GetCurrentSigningMaterialAsync();
        Assert.Equal(keys.KeyId, signed.KeyId);
        Assert.True(Ed25519SigningKeyStore.Verify(keys.PublicKey, payload, signed.Signature));
    }

    [Fact]
    public async Task TestCatalogUsesTLSFQDNAsServerName()
    {
        var (seed, _, req) = await RegisterAsync("node-fqdn");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerName = "fi-hel-01.nyxveil.ru";
        retry.Endpoints =
        [
            new NodeEndpointDto { Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 1, Enabled = true }
        ];
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var token = await CreateUserLicenseTokenAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var node = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == "node-fqdn");
        Assert.Equal("fi-hel-01.nyxveil.ru", node.ServerName);
        Assert.Equal("46.8.218.27", Assert.Single(node.Endpoints).Host);
    }

    [Fact]
    public async Task TestCatalogEndpointProjectionIsUnique()
    {
        var boot = await CreateBootstrapAsync();
        var (seed, pub) = GenerateEd25519();
        var req = NewNodeRequest(boot.BootstrapToken, "node-dup-ep");
        req.PublicKey = pub;
        req.Endpoints =
        [
            new NodeEndpointDto { Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 1, Enabled = true },
            new NodeEndpointDto { Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 2, Enabled = true }
        ];
        await _fx.Nodes.RegisterWithBootstrapAsync(req);

        Assert.Equal(1, await _fx.Db.NodeEndpoints.CountAsync(e => e.NodeId == "node-dup-ep"));

        var token = await CreateUserLicenseTokenAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var node = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == "node-dup-ep");
        Assert.Single(node.Endpoints);
    }

    [Fact]
    public async Task TestCatalogCacheInvalidatedAfterNodeReregister()
    {
        var (seed, _, req) = await RegisterAsync("node-nocache");
        var token = await CreateUserLicenseTokenAsync();
        var before = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.Equal("1.0.1", Assert.Single(before.Catalog.Nodes, n => n.NodeId == "node-nocache").ServerVersion);

        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerVersion = "1.0.10";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var after = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.Equal("1.0.10", Assert.Single(after.Catalog.Nodes, n => n.NodeId == "node-nocache").ServerVersion);
        Assert.NotEqual(before.Catalog.Version, after.Catalog.Version);
    }

    [Fact]
    public async Task TestCatalogSignatureValidAfterNodeMetadataUpdate()
    {
        var (seed, _, req) = await RegisterAsync("node-sig");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerVersion = "1.0.10";
        retry.ServerName = "fi-hel-01.nyxveil.ru";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var token = await CreateUserLicenseTokenAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.NotEmpty(signed.Signature);

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(signed.Catalog);
        var keys = await _fx.Scope.ServiceProvider.GetRequiredService<ISigningKeyService>()
            .GetCurrentSigningMaterialAsync();
        Assert.Equal(keys.KeyId, signed.KeyId);
        Assert.True(Ed25519SigningKeyStore.Verify(keys.PublicKey, payload, signed.Signature));
    }

    [Fact]
    public async Task TestSameNodeReregisterDoesNotCreateDuplicateNode()
    {
        var (seed, _, req) = await RegisterAsync("node-once");
        var retry = await CloneBootstrapPoPAsync(req, seed);
        retry.ServerVersion = "1.0.10";
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);
        Assert.Equal(1, await _fx.Db.Nodes.CountAsync(n => n.NodeId == "node-once"));
    }

    [Fact]
    public void TestEndpointDeduplicateKeepsLowestPriority()
    {
        var got = NodeRegistrationService.DeduplicateEndpoints(
        [
            new NodeEndpointDto { Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 2, Enabled = true },
            new NodeEndpointDto { Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 1, Enabled = true }
        ]);
        var ep = Assert.Single(got);
        Assert.Equal(1, ep.Priority);
    }

    private async Task<(byte[] Seed, byte[] Pub, NodeRegisterRequest Req)> RegisterAsync(string nodeId)
    {
        var boot = await CreateBootstrapAsync();
        var (seed, pub) = GenerateEd25519();
        var req = NewNodeRequest(boot.BootstrapToken, nodeId);
        req.PublicKey = pub;
        req.ServerVersion = "1.0.1";
        req.ServerName = "46.8.218.27";
        await _fx.Nodes.RegisterWithBootstrapAsync(req);
        return (seed, pub, req);
    }

    private async Task<NodeRegisterRequest> CloneBootstrapPoPAsync(NodeRegisterRequest original, byte[] seed)
    {
        var boot = await CreateBootstrapAsync();
        return new()
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = original.NodeId,
            LocationId = original.LocationId,
            DisplayName = original.DisplayName,
            PublicIdentity = original.PublicIdentity,
            PublicKey = original.PublicKey,
            ServerName = original.ServerName,
            SpkiPin = original.SpkiPin,
            ProtocolVersion = original.ProtocolVersion,
            ServerVersion = original.ServerVersion,
            Capacity = original.Capacity,
            TestOnly = original.TestOnly,
            Endpoints = original.Endpoints.Select(e => new NodeEndpointDto
            {
                Host = e.Host,
                Port = e.Port,
                AddressFamily = e.AddressFamily,
                Priority = e.Priority,
                Enabled = e.Enabled
            }).ToList(),
            NodeToken = CoreNodeToken.Sign(original.NodeId, seed, _fx.Clock.UtcNow)
        };
    }

    private Task<CreateBootstrapTokenResponse> CreateBootstrapAsync(int maxUses = 3) =>
        _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = maxUses,
            CreatedBy = "test"
        });

    private NodeRegisterRequest NewNodeRequest(string bootstrapToken, string nodeId) => new()
    {
        BootstrapToken = bootstrapToken,
        NodeId = nodeId,
        LocationId = _fx.LocationId,
        DisplayName = nodeId,
        PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
        PublicKey = ControlPlaneTestFixture.RandomKey32(),
        ServerName = nodeId + ".example",
        SpkiPin = ControlPlaneTestFixture.RandomKey32(),
        ProtocolVersion = 1,
        ServerVersion = "1.0.1",
        Capacity = 100,
        Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Priority = 1, Enabled = true }]
    };

    private async Task<string> CreateUserLicenseTokenAsync()
    {
        var created = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "user",
            MaxDevices = 5,
            AllowedLocations = new[] { _fx.LocationId },
            CreatedBy = "test"
        });
        return created.LicenseToken;
    }

    private static (byte[] Seed, byte[] PublicKey) GenerateEd25519()
    {
        using var key = Key.Create(
            SignatureAlgorithm.Ed25519,
            new KeyCreationParameters { ExportPolicy = KeyExportPolicies.AllowPlaintextExport });
        return (key.Export(KeyBlobFormat.RawPrivateKey), key.PublicKey.Export(KeyBlobFormat.RawPublicKey));
    }
}
