using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Tickets;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>Frozen Core NVP/1 interop + security regression tests.</summary>
public sealed class FrozenCoreInteropTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();

    public ValueTask DisposeAsync() => _fx.DisposeAsync();

    [Fact]
    public async Task TestDefaultTicketAudienceMatchesFrozenCore()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync();
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Equal(new[] { "nvp-node" }, claims.Audience);
    }

    [Fact]
    public async Task TestLicenseCodeNormalizedToLocationId()
    {
        var created = await _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            AllowedLocations = new[] { _fx.LocationCode },
            CreatedBy = "test"
        });

        Assert.True(Application.Common.LicenseIdFormat.TryParse(created.LicenseId, out var licId));
        var row = await _fx.Db.LicenseAllowedLocations.SingleAsync(a => a.LicenseId == licId);
        Assert.Equal(_fx.LocationId, row.LocationId);
        Assert.NotEqual(_fx.LocationCode, row.LocationId);
    }

    [Fact]
    public async Task TestTicketIssueAcceptsFrozenCoreCatalogLocationId()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(locations: new[] { _fx.LocationCode });
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            LocationId = _fx.LocationId
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Equal(new[] { _fx.LocationId }, claims.Locations);
    }

    [Fact]
    public async Task TestTicketLocationsContainCanonicalLocationIds()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(locations: new[] { _fx.LocationCode, _fx.LocationCodeB });
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Equal(new[] { _fx.LocationId, _fx.LocationIdB }, claims.Locations);
        Assert.DoesNotContain(_fx.LocationCode, claims.Locations!);
    }

    [Fact]
    public async Task TestLocationCodeIsNotEmittedAsTicketSecurityScope()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(locations: new[] { _fx.LocationCode });
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            LocationId = _fx.LocationCode // admin alias input
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Equal(new[] { _fx.LocationId }, claims.Locations);
        Assert.DoesNotContain(_fx.LocationCode, claims.Locations!);
    }

    [Fact]
    public async Task TestRefreshUsesCanonicalLocationIds()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(locations: new[] { _fx.LocationId, _fx.LocationIdB });
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            LocationId = _fx.LocationId
        });

        var refreshed = await _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = issued.AccessTicket
        });

        var claims = _fx.TicketIssuer.VerifyAccessTicket(refreshed.AccessTicket);
        Assert.Equal(new[] { _fx.LocationId }, claims.Locations);
    }

    [Fact]
    public async Task TestWrongIssuerRejectedByControlPlane()
    {
        var jwt = await IssueRawAsync(iss: "evil-issuer");
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestWrongAudienceRejectedByControlPlane()
    {
        var jwt = await IssueRawAsync(aud: "nvp-nodes");
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestNotBeforeRejected()
    {
        var now = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        var jwt = await IssueRawAsync(nbf: now + 3600);
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestMissingJtiRejected()
    {
        var jwt = await IssueRawAsync(omitJti: true);
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestMissingDevicePubRejected()
    {
        var jwt = await IssueRawAsync(omitDevicePub: true);
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestWrongProtocolRejected()
    {
        var jwt = await IssueRawAsync(protocol: "NVP/0");
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestExpiredRejected()
    {
        var now = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        var jwt = await IssueRawAsync(exp: now - 10, iat: now - 100, nbf: now - 100);
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestUnknownKidRejected()
    {
        var jwt = await IssueRawAsync(kid: "unknown-kid");
        Assert.Throws<UnauthorizedAccessException>(() => _fx.TicketIssuer.VerifyAccessTicket(jwt));
    }

    [Fact]
    public async Task TestDeviceActivationSameKeyIdempotent()
    {
        var created = await CreateLicenseAsync();
        var pk = ControlPlaneTestFixture.RandomKey32();
        var req = new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = "dev-same",
            PublicKey = pk
        };
        await _fx.Devices.ActivateAsync(req);
        var again = await _fx.Devices.ActivateAsync(req);
        Assert.True(again.Activated);
        Assert.Equal(1, await _fx.Db.Devices.CountAsync());
    }

    [Fact]
    public async Task TestDeviceActivationDifferentKeyRejected()
    {
        var created = await CreateLicenseAsync();
        var deviceId = "dev-rebind";
        await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        });

        await Assert.ThrowsAsync<ConflictException>(() => _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        }));
    }

    [Fact]
    public async Task TestDevicePublicKeyCannotBeReboundWithLicenseTokenOnly()
    {
        await TestDeviceActivationDifferentKeyRejected();
    }

    [Fact]
    public async Task TestExistingNodeCannotReregisterWithoutProof()
    {
        var boot = await CreateBootstrapAsync();
        var req = NewNodeRequest(boot.BootstrapToken, "node-pop");
        await _fx.Nodes.RegisterWithBootstrapAsync(req);

        var retry = NewNodeRequest(boot.BootstrapToken, "node-pop");
        retry.PublicIdentity = req.PublicIdentity;
        retry.PublicKey = req.PublicKey;
        await Assert.ThrowsAsync<ForbiddenException>(() =>
            _fx.Nodes.RegisterWithBootstrapAsync(retry));
    }

    [Fact]
    public async Task TestExistingNodeCannotReplacePublicKey()
    {
        var boot = await CreateBootstrapAsync(maxUses: 3);
        var req = NewNodeRequest(boot.BootstrapToken, "node-key");
        var first = await _fx.Nodes.RegisterWithBootstrapAsync(req);

        var retry = NewNodeRequest(boot.BootstrapToken, "node-key");
        retry.PublicIdentity = req.PublicIdentity;
        retry.PublicKey = ControlPlaneTestFixture.RandomKey32();
        retry.NodeToken = first.NodeToken;

        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Nodes.RegisterWithBootstrapAsync(retry));
    }

    [Fact]
    public async Task TestExistingNodeCannotObtainNewCredentialByKnowingPublicIdentity()
    {
        var boot = await CreateBootstrapAsync(maxUses: 3);
        var req = NewNodeRequest(boot.BootstrapToken, "node-takeover");
        var first = await _fx.Nodes.RegisterWithBootstrapAsync(req);
        var originalVerifier = (await _fx.Db.NodeCredentials.SingleAsync(c => c.NodeId == "node-takeover"))
            .NodeAuthSecretVerifier;

        var retry = NewNodeRequest(boot.BootstrapToken, "node-takeover");
        retry.PublicIdentity = req.PublicIdentity;
        retry.PublicKey = req.PublicKey;
        await Assert.ThrowsAsync<ForbiddenException>(() =>
            _fx.Nodes.RegisterWithBootstrapAsync(retry));

        var after = await _fx.Db.NodeCredentials.SingleAsync(c => c.NodeId == "node-takeover");
        Assert.Equal(originalVerifier, after.NodeAuthSecretVerifier);
        Assert.False(string.IsNullOrEmpty(first.NodeToken));
    }

    [Fact]
    public async Task TestExistingNodeProofOfPossessionAllowsIdempotentRetry()
    {
        var boot = await CreateBootstrapAsync(maxUses: 3);
        var req = NewNodeRequest(boot.BootstrapToken, "node-retry");
        var first = await _fx.Nodes.RegisterWithBootstrapAsync(req);

        var retry = NewNodeRequest(boot.BootstrapToken, "node-retry");
        retry.PublicIdentity = req.PublicIdentity;
        retry.PublicKey = req.PublicKey;
        retry.NodeToken = first.NodeToken;
        var second = await _fx.Nodes.RegisterWithBootstrapAsync(retry);

        Assert.True(second.Registered);
        Assert.Equal(string.Empty, second.NodeToken);
        Assert.Equal(1, await _fx.Db.Nodes.CountAsync(n => n.NodeId == "node-retry"));
    }

    [Fact]
    public async Task TestBootstrapOnlyCreatesNewNode()
    {
        var boot = await CreateBootstrapAsync();
        var registered = await _fx.Nodes.RegisterWithBootstrapAsync(NewNodeRequest(boot.BootstrapToken, "node-new"));
        Assert.True(registered.Registered);
        Assert.Equal(1, await _fx.Db.Nodes.CountAsync());
    }

    [Fact]
    public async Task TestBootstrapCannotResetExistingNodeIdentity()
    {
        var boot = await CreateBootstrapAsync(maxUses: 3);
        var req = NewNodeRequest(boot.BootstrapToken, "node-id");
        await _fx.Nodes.RegisterWithBootstrapAsync(req);

        var other = NewNodeRequest(boot.BootstrapToken, "node-id");
        other.PublicIdentity = ControlPlaneTestFixture.RandomKey32();
        await Assert.ThrowsAsync<ConflictException>(() => _fx.Nodes.RegisterWithBootstrapAsync(other));
    }

    [Fact]
    public async Task TestFrozenCoreNodeTokenHeartbeatSucceeds()
    {
        var (seed, pub) = GenerateEd25519();
        var boot = await CreateBootstrapAsync();
        var req = NewNodeRequest(boot.BootstrapToken, "node-core-tok");
        req.PublicKey = pub;
        await _fx.Nodes.RegisterWithBootstrapAsync(req);

        var unix = new DateTimeOffset(_fx.Clock.UtcNow).ToUnixTimeSeconds();
        var token = NodeAuthService.SignCoreNodeTokenV1("node-core-tok", seed, unix);

        await _fx.NodeAuth.ValidateNodeRequestAsync(
            "node-core-tok",
            new Dictionary<string, string> { ["Authorization"] = "Bearer " + token });

        var hb = await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = "node-core-tok",
            CurrentSessions = 3
        });
        Assert.True(hb.Accepted);

        // Replay rejected.
        await Assert.ThrowsAsync<UnauthorizedException>(() =>
            _fx.NodeAuth.ValidateNodeRequestAsync(
                "node-core-tok",
                new Dictionary<string, string> { ["Authorization"] = "Bearer " + token }));
    }

    [Fact]
    public async Task TestHeartbeatDoesNotAuthenticateTwice()
    {
        var (nodeId, token, _, _) = await RegisterNodeAsync("node-once");
        await _fx.NodeAuth.ValidateNodeRequestAsync(
            nodeId,
            new Dictionary<string, string> { ["Authorization"] = "Bearer " + token });

        // Service accepts without NodeToken — auth already done at boundary.
        var hb = await _fx.Heartbeats.ProcessHeartbeatAsync(new NodeHeartbeatRequest
        {
            NodeId = nodeId,
            CurrentSessions = 1
        });
        Assert.True(hb.Accepted);
    }

    [Fact]
    public async Task TestHeartbeatWrongNodeRejected()
    {
        var (_, tokenA, _, _) = await RegisterNodeAsync("node-x");
        await RegisterNodeAsync("node-y");

        await Assert.ThrowsAsync<UnauthorizedException>(() =>
            _fx.NodeAuth.ValidateNodeRequestAsync(
                "node-y",
                new Dictionary<string, string> { ["Authorization"] = "Bearer " + tokenA }));
    }

    [Fact]
    public async Task TestCatalogProfilesFromTransports()
    {
        await RegisterNodeAsync("node-prof");
        var (token, _, _) = await CreateLicensedDeviceAsync();
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        var node = Assert.Single(signed.Catalog.Nodes, n => n.NodeId == "node-prof");
        var ep = Assert.Single(node.Endpoints);
        Assert.Contains("quic-udp-443", ep.Profiles);
        Assert.Contains("tls-tcp-443", ep.Profiles);
        Assert.Equal("dual", ep.IpFamily);
    }

    [Fact]
    public async Task TestPlanPermissionsUsedForTicket()
    {
        var plan = await _fx.Db.Plans.SingleAsync(p => p.PlanId == _fx.StandardPlanId);
        plan.Permissions = """["connect","metrics"]""";
        await _fx.Db.SaveChangesAsync();

        var (token, deviceId, _) = await CreateLicensedDeviceAsync();
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });
        var claims = _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket);
        Assert.Contains("connect", claims.Permissions);
        Assert.Contains("metrics", claims.Permissions);
    }

    [Fact]
    public async Task TestMissingConnectPermissionPreventsTicketIssue()
    {
        var plan = await _fx.Db.Plans.SingleAsync(p => p.PlanId == _fx.StandardPlanId);
        plan.Permissions = """["metrics"]""";
        await _fx.Db.SaveChangesAsync();

        var (token, deviceId, _) = await CreateLicensedDeviceAsync();
        await Assert.ThrowsAsync<ForbiddenException>(() => _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        }));
    }

    [Fact]
    public async Task TestRefreshDropsRemovedPermission()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync();
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });

        var plan = await _fx.Db.Plans.SingleAsync(p => p.PlanId == _fx.StandardPlanId);
        plan.Permissions = """["connect","extra"]""";
        await _fx.Db.SaveChangesAsync();
        _fx.Db.ChangeTracker.Clear();

        var refreshed = await _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = issued.AccessTicket
        });
        var claims = _fx.TicketIssuer.VerifyAccessTicket(refreshed.AccessTicket);
        Assert.Contains("extra", claims.Permissions);

        plan = await _fx.Db.Plans.SingleAsync(p => p.PlanId == _fx.StandardPlanId);
        plan.Permissions = """["connect"]""";
        await _fx.Db.SaveChangesAsync();
        _fx.Db.ChangeTracker.Clear();

        var again = await _fx.Tickets.RefreshAsync(new TicketRefreshRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            AccessTicket = refreshed.AccessTicket
        });
        Assert.DoesNotContain("extra", _fx.TicketIssuer.VerifyAccessTicket(again.AccessTicket).Permissions);
    }

    [Fact]
    public async Task TestLicenseRolePreserved()
    {
        var (token, deviceId, _) = await CreateLicensedDeviceAsync(role: "test");
        var issued = await _fx.Tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId
        });
        Assert.Equal("test", _fx.TicketIssuer.VerifyAccessTicket(issued.AccessTicket).Role);
    }

    [Fact]
    public async Task TestTestRoleCannotSeeTestNodes()
    {
        await SeedTestNodesAsync();
        var (token, _, _) = await CreateLicensedDeviceAsync(role: "test");
        var signed = await _fx.Catalog.GetSignedCatalogForCallerAsync(null, token);
        Assert.DoesNotContain(signed.Catalog.Nodes, n => n.TestOnly);
    }

    [Fact]
    public void TestExactHostname()
    {
        using var cert = CertificateLoader.CreateSelfSigned("exact.nyxveil.local");
        Assert.True(cert.MatchesHostname("exact.nyxveil.local"));
    }

    [Fact]
    public void TestWildcardMatchesOneLabel()
    {
        using var ecdsa = ECDsa.Create(ECCurve.NamedCurves.nistP256);
        var req = new CertificateRequest("CN=wildcard", ecdsa, HashAlgorithmName.SHA256);
        var san = new SubjectAlternativeNameBuilder();
        san.AddDnsName("*.example.com");
        req.CertificateExtensions.Add(san.Build());
        using var cert = req.CreateSelfSigned(DateTimeOffset.UtcNow.AddDays(-1), DateTimeOffset.UtcNow.AddDays(30));
        Assert.True(cert.MatchesHostname("api.example.com"));
        Assert.False(cert.MatchesHostname("a.b.example.com"));
    }

    [Fact]
    public void TestWrongHostnameRejected()
    {
        using var cert = CertificateLoader.CreateSelfSigned("right.nyxveil.local");
        Assert.False(cert.MatchesHostname("wrong.nyxveil.local"));
    }

    private async Task<(string Token, string DeviceId, Guid LicenseId)> CreateLicensedDeviceAsync(
        string role = "user",
        IReadOnlyList<string>? locations = null)
    {
        var created = await CreateLicenseAsync(role, locations);
        var deviceId = "dev-" + Guid.NewGuid().ToString("N")[..8];
        await _fx.Devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = created.LicenseToken,
            DeviceId = deviceId,
            PublicKey = ControlPlaneTestFixture.RandomKey32()
        });
        Assert.True(Application.Common.LicenseIdFormat.TryParse(created.LicenseId, out var licenseId));
        return (created.LicenseToken, deviceId, licenseId);
    }

    private Task<CreateLicenseResponse> CreateLicenseAsync(
        string role = "user",
        IReadOnlyList<string>? locations = null) =>
        _fx.Licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = role,
            MaxDevices = 5,
            AllowedLocations = locations ?? new[] { _fx.LocationId },
            CreatedBy = "test"
        });

    private Task<CreateBootstrapTokenResponse> CreateBootstrapAsync(int maxUses = 1) =>
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
        SpkiPin = ControlPlaneTestFixture.RandomKey32(),
        Endpoints = [new NodeEndpointDto { Host = nodeId + ".example", Port = 443, Priority = 1 }]
    };

    private async Task<(string NodeId, string Token, byte[] Identity, byte[]? Spki)> RegisterNodeAsync(string nodeId)
    {
        var boot = await CreateBootstrapAsync(maxUses: 3);
        var req = NewNodeRequest(boot.BootstrapToken, nodeId);
        var registered = await _fx.Nodes.RegisterWithBootstrapAsync(req);
        return (registered.NodeId, registered.NodeToken, req.PublicIdentity, req.SpkiPin);
    }

    private async Task SeedTestNodesAsync()
    {
        var now = _fx.Clock.UtcNow;
        if (!await _fx.Db.Nodes.AnyAsync(n => n.NodeId == "node-test-vis"))
        {
            _fx.Db.Nodes.Add(new Node
            {
                NodeId = "node-test-vis",
                LocationId = _fx.LocationId,
                DisplayName = "Test",
                Enabled = true,
                TestOnly = true,
                PublicIdentity = ControlPlaneTestFixture.RandomKey32(),
                Capacity = 10,
                CreatedAt = now,
                UpdatedAt = now,
                ConfigVersion = 1,
                Transports =
                [
                    new NodeTransport
                    {
                        Id = Guid.NewGuid(),
                        NodeId = "node-test-vis",
                        TransportType = "tls",
                        Enabled = true,
                        Priority = 1
                    }
                ]
            });
            _fx.Db.NodeConfigs.Add(new NodeConfig
            {
                NodeId = "node-test-vis",
                Enabled = true,
                Capacity = 10,
                TransportPolicyJson = "{}",
                ConfigVersion = 1,
                UpdatedAt = now
            });
            await _fx.Db.SaveChangesAsync();
        }
    }

    private static (byte[] Seed, byte[] PublicKey) GenerateEd25519()
    {
        using var key = Key.Create(
            SignatureAlgorithm.Ed25519,
            new KeyCreationParameters { ExportPolicy = KeyExportPolicies.AllowPlaintextExport });
        return (key.Export(KeyBlobFormat.RawPrivateKey), key.PublicKey.Export(KeyBlobFormat.RawPublicKey));
    }

    private async Task<string> IssueRawAsync(
        string? iss = null,
        string? aud = null,
        string? kid = null,
        string? protocol = "NVP/1",
        long? exp = null,
        long? iat = null,
        long? nbf = null,
        bool omitJti = false,
        bool omitDevicePub = false)
    {
        var material = await _fx.Scope.ServiceProvider
            .GetRequiredService<Application.Abstractions.ISigningKeyService>()
            .GetCurrentSigningMaterialAsync();

        var now = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        var payload = new Dictionary<string, object?>
        {
            ["iss"] = iss ?? "nyxveil-control-plane-test",
            ["aud"] = new[] { aud ?? "nvp-node" },
            ["iat"] = iat ?? now,
            ["nbf"] = nbf ?? now,
            ["exp"] = exp ?? now + 900,
            ["license_id"] = "nyx_lic_test",
            ["device_id"] = "dev_test",
            ["role"] = "user",
            ["plan"] = "standard",
            ["permissions"] = new[] { "connect" },
            ["protocol_version"] = protocol
        };
        if (!omitJti)
            payload["jti"] = "tkt_test";
        if (!omitDevicePub)
            payload["device_pub"] = ControlPlaneTestFixture.RandomKey32();

        var header = new Dictionary<string, object>
        {
            ["alg"] = "EdDSA",
            ["typ"] = "JWT",
            ["kid"] = kid ?? material.KeyId
        };

        var headerB64 = AccessTicketService.Base64UrlEncode(JsonSerializer.SerializeToUtf8Bytes(header));
        var payloadB64 = AccessTicketService.Base64UrlEncode(JsonSerializer.SerializeToUtf8Bytes(payload));
        var signingInput = Encoding.ASCII.GetBytes(headerB64 + "." + payloadB64);
        var sig = Ed25519SigningKeyStore.Sign(material.PrivateKey, signingInput);
        return headerB64 + "." + payloadB64 + "." + AccessTicketService.Base64UrlEncode(sig);
    }
}
