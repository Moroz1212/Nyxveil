using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.UnitTests.Helpers;
using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Live defect #2: stale catalog advertisement after node upgrade.
/// Existing-node PoP re-registration refreshes mutable ads without touching admin fields.
/// </summary>
public sealed class StaleAdvertisementLifecycleTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    public ValueTask DisposeAsync() => _fx.DisposeAsync();

    [Fact]
    public async Task ExistingNodePopReregister_RefreshesAdvertisement_AndPreservesAdminFields()
    {
        const string nodeId = "nv-test-227e939e";
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 3,
            CreatedBy = "test",
            AllowedLocation = _fx.LocationId
        });

        // Ensure location looks like production Helsinki for this test identity.
        var loc = await _fx.Db.Locations.SingleAsync(l => l.LocationId == _fx.LocationId);
        // Keep fixture location id; advertisement refresh must not change LocationId.

        var (seed, pub) = GenerateEd25519();
        var identity = ControlPlaneTestFixture.RandomKey32();
        var oldSpki = Convert.FromBase64String("qkH8Lhu9eG+m3xo08MnmRUMZV8sLBQRQUD9baxqSRWA=");
        var initial = new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = identity,
            PublicKey = pub,
            ServerVersion = "1.0.1",
            ServerName = "46.8.218.27",
            SpkiPin = oldSpki,
            ProtocolVersion = 1,
            Capacity = 100,
            TestOnly = false,
            Endpoints =
            [
                new NodeEndpointDto
                {
                    Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 1, Enabled = true
                }
            ]
        };
        await _fx.Nodes.RegisterWithBootstrapAsync(initial);

        var before = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId);
        Assert.Equal("1.0.1", before.ServerVersion);
        Assert.Equal("46.8.218.27", before.ServerName);
        Assert.Equal(oldSpki, before.SpkiPin);
        Assert.True(before.Enabled);
        Assert.False(before.TestOnly);
        Assert.False(before.Draining);
        var beforeIdentity = before.PublicIdentity.ToArray();
        var beforeCred = (await _fx.Db.NodeCredentials.SingleAsync(c => c.NodeId == nodeId)).PublicKey.ToArray();
        var beforeLoc = before.LocationId;

        var newSpki = Convert.FromBase64String("Y4VRkarf5cFKyESDcgYl5Fkl1UI4gaoxcfXFMVdsRIg=");
        var retry = new NodeRegisterRequest
        {
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = "fi-hel-01",
            PublicIdentity = identity,
            PublicKey = pub,
            ServerVersion = "1.1.6",
            ServerName = "fi-hel-01.nyxveil.ru",
            SpkiPin = newSpki,
            ProtocolVersion = 1,
            Capacity = 100,
            TestOnly = true, // must be ignored for existing node
            Endpoints =
            [
                new NodeEndpointDto
                {
                    Host = "fi-hel-01.nyxveil.ru", Port = 443, AddressFamily = "hostname", Priority = 1,
                    Enabled = true
                }
            ],
            NodeToken = CoreNodeToken.Sign(nodeId, seed, _fx.Clock.UtcNow)
        };
        await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        var after = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == nodeId);
        Assert.Equal("1.1.6", after.ServerVersion);
        Assert.Equal("fi-hel-01.nyxveil.ru", after.ServerName);
        Assert.Equal(newSpki, after.SpkiPin);
        Assert.Equal(beforeLoc, after.LocationId);
        Assert.Equal(beforeIdentity, after.PublicIdentity);
        Assert.True(after.Enabled);
        Assert.False(after.TestOnly);
        Assert.False(after.Draining);
        Assert.Equal(beforeCred, (await _fx.Db.NodeCredentials.SingleAsync(c => c.NodeId == nodeId)).PublicKey);

        var eps = await _fx.Db.NodeEndpoints.Where(e => e.NodeId == nodeId).ToListAsync();
        Assert.Contains(eps, e => e.Host == "fi-hel-01.nyxveil.ru" && e.Port == 443);
        Assert.DoesNotContain(eps, e => e.Host == "46.8.218.27");

        var token = await CreateLicenseTokenAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var node = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == nodeId);
        Assert.Equal("1.1.6", node.ServerVersion);
        Assert.Equal("fi-hel-01.nyxveil.ru", node.ServerName);
        Assert.Equal(newSpki, node.SpkiPin);
        Assert.Equal("fi-hel-01.nyxveil.ru", Assert.Single(node.Endpoints).Host);

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(signed.Catalog);
        var keys = await _fx.Scope.ServiceProvider.GetRequiredService<ISigningKeyService>()
            .GetCurrentSigningMaterialAsync();
        Assert.True(Ed25519SigningKeyStore.Verify(keys.PublicKey, payload, signed.Signature));
    }

    [Fact]
    public async Task WrongPop_IsRejected()
    {
        var (seed, pub, req) = await RegisterAsync("node-bad-pop");
        var other = GenerateEd25519().Seed;
        var retry = Clone(req, other);
        retry.ServerVersion = "9.9.9";
        await Assert.ThrowsAsync<UnauthorizedException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
        Assert.Equal("1.0.1", (await _fx.Db.Nodes.SingleAsync(n => n.NodeId == "node-bad-pop")).ServerVersion);
    }

    [Fact]
    public async Task DifferentPublicIdentity_IsRejected()
    {
        var (seed, _, req) = await RegisterAsync("node-bad-id");
        var retry = Clone(req, seed);
        retry.PublicIdentity = ControlPlaneTestFixture.RandomKey32();
        retry.ServerVersion = "9.9.9";
        await Assert.ThrowsAsync<ConflictException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
    }

    [Fact]
    public async Task LocationChangeAttempt_IsRejected()
    {
        var (seed, _, req) = await RegisterAsync("node-bad-loc");
        var retry = Clone(req, seed);
        retry.LocationId = _fx.LocationIdB;
        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
    }

    [Fact]
    public async Task CredentialReplacement_IsRejected()
    {
        var (seed, _, req) = await RegisterAsync("node-bad-cred");
        var retry = Clone(req, seed);
        retry.PublicKey = ControlPlaneTestFixture.RandomKey32();
        retry.ServerVersion = "9.9.9";
        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
    }

    [Theory]
    [InlineData(NodeLifecycleState.Deleted)]
    [InlineData(NodeLifecycleState.Revoked)]
    public async Task DeletedOrRevoked_IsRejected(NodeLifecycleState state)
    {
        var (seed, _, req) = await RegisterAsync("node-dead-" + state);
        var node = await _fx.Db.Nodes.SingleAsync(n => n.NodeId == req.NodeId);
        node.LifecycleState = state;
        await _fx.Db.SaveChangesAsync();
        var retry = Clone(req, seed);
        retry.ServerVersion = "9.9.9";
        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
    }

    private async Task<(byte[] Seed, byte[] Pub, NodeRegisterRequest Req)> RegisterAsync(string nodeId)
    {
        var boot = await _fx.Bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = _fx.Clock.UtcNow.AddHours(1),
            MaxUses = 3,
            CreatedBy = "test"
        });
        var (seed, pub) = GenerateEd25519();
        var req = new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
            PublicKey = pub,
            ServerVersion = "1.0.1",
            ServerName = "46.8.218.27",
            SpkiPin = ControlPlaneTestFixture.RandomKey32(),
            ProtocolVersion = 1,
            Capacity = 100,
            Endpoints =
            [
                new NodeEndpointDto { Host = "46.8.218.27", Port = 443, AddressFamily = "ipv4", Priority = 1, Enabled = true }
            ]
        };
        await _fx.Nodes.RegisterWithBootstrapAsync(req);
        return (seed, pub, req);
    }

    private NodeRegisterRequest Clone(NodeRegisterRequest original, byte[] seed) => new()
    {
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
        Endpoints = original.Endpoints.ToList(),
        NodeToken = CoreNodeToken.Sign(original.NodeId, seed, _fx.Clock.UtcNow)
    };

    private async Task<string> CreateLicenseTokenAsync()
    {
        var created = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "user",
            MaxDevices = 5,
            AllowedLocations = [_fx.LocationId],
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
