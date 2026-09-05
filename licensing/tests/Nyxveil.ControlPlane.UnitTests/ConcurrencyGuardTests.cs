using System.Security.Cryptography;
using Microsoft.EntityFrameworkCore;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Concurrency / anti-replay reject-path tests (InMemory approximates MSSQL UPDLOCK / atomic UPDATE).
/// Full race coverage is MSSQL-oriented; see scripts/test-database.ps1 for live SQL gates.
/// </summary>
public sealed class ConcurrencyGuardTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();

    public ValueTask DisposeAsync() => _fx.DisposeAsync();

    [Fact]
    public async Task TestCoreNodeTokenReplayRejected()
    {
        var seed = RandomNumberGenerator.GetBytes(32);
        using var key = Key.Import(SignatureAlgorithm.Ed25519, seed, KeyBlobFormat.RawPrivateKey);
        var pub = key.Export(KeyBlobFormat.RawPublicKey);

        const string nodeId = "node-replay-1";
        _fx.Db.Nodes.Add(new Node
        {
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            Enabled = true,
            PublicIdentity = RandomNumberGenerator.GetBytes(32),
            Capacity = 10,
            CreatedAt = _fx.Clock.UtcNow,
            UpdatedAt = _fx.Clock.UtcNow,
            ConfigVersion = 1
        });
        _fx.Db.NodeCredentials.Add(new NodeCredential
        {
            NodeId = nodeId,
            PublicKey = pub,
            CredentialIssuedAt = _fx.Clock.UtcNow
        });
        await _fx.Db.SaveChangesAsync();

        var token = CoreNodeToken.Sign(nodeId, seed, _fx.Clock.UtcNow);
        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
        {
            ["Authorization"] = "Bearer " + token
        };

        await _fx.NodeAuth.VerifyCoreNodeTokenV1Async(nodeId, token);
        await Assert.ThrowsAsync<UnauthorizedException>(() =>
            _fx.NodeAuth.VerifyCoreNodeTokenV1Async(nodeId, token));
    }

    [Fact]
    public async Task TestCoreNodeTokenOlderTimestampRejected()
    {
        var seed = RandomNumberGenerator.GetBytes(32);
        using var key = Key.Import(SignatureAlgorithm.Ed25519, seed, KeyBlobFormat.RawPrivateKey);
        var pub = key.Export(KeyBlobFormat.RawPublicKey);

        const string nodeId = "node-replay-2";
        _fx.Db.Nodes.Add(new Node
        {
            NodeId = nodeId,
            LocationId = _fx.LocationId,
            DisplayName = nodeId,
            Enabled = true,
            PublicIdentity = RandomNumberGenerator.GetBytes(32),
            Capacity = 10,
            CreatedAt = _fx.Clock.UtcNow,
            UpdatedAt = _fx.Clock.UtcNow,
            ConfigVersion = 1
        });
        _fx.Db.NodeCredentials.Add(new NodeCredential
        {
            NodeId = nodeId,
            PublicKey = pub,
            CredentialIssuedAt = _fx.Clock.UtcNow,
            LastCoreTokenUnix = DateTimeOffset.UtcNow.ToUnixTimeSeconds()
        });
        await _fx.Db.SaveChangesAsync();

        // Token at "now" but LastCoreTokenUnix already equals/exceeds → replay.
        var token = CoreNodeToken.Sign(nodeId, seed, _fx.Clock.UtcNow);
        await Assert.ThrowsAsync<UnauthorizedException>(() =>
            _fx.NodeAuth.VerifyCoreNodeTokenV1Async(nodeId, token));
    }

    [Fact]
    public void TestCoreNodeTokenLooksLikeRejectsLegacyBearer()
    {
        Assert.False(CoreNodeToken.LooksLike("nvpnode_node1_deadbeef"));
        Assert.True(CoreNodeToken.LooksLike("1710000000.abc_def-ghi"));
    }

    [Fact]
    public async Task TestDeviceActivateSerializablePathDoesNotThrowOnInMemory()
    {
        // Documents InMemory limitation: UPDLOCK branch skipped; Serializable transaction is no-op warned.
        var created = await _fx.Licenses.CreateLicenseAsync(new Application.Contracts.V1.CreateLicenseRequest
        {
            PlanId = _fx.StandardPlanId,
            Role = "user",
            MaxDevices = 1,
            AllowedLocations = new[] { _fx.LocationCode },
            CreatedBy = "test"
        });
        var token = created.LicenseToken;
        var pk = RandomNumberGenerator.GetBytes(32);
        await _fx.Devices.ActivateAsync(new Application.Contracts.V1.DeviceActivateRequest
        {
            LicenseToken = token,
            DeviceId = "dev-a",
            PublicKey = pk
        });
        await Assert.ThrowsAsync<ConflictException>(() =>
            _fx.Devices.ActivateAsync(new Application.Contracts.V1.DeviceActivateRequest
            {
                LicenseToken = token,
                DeviceId = "dev-b",
                PublicKey = RandomNumberGenerator.GetBytes(32)
            }));
    }
}
