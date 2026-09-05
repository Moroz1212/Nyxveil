using System.Net;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.Infrastructure.Services;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class NodeRequestSecurityTests : IClassFixture<CustomWebApplicationFactory>, IAsyncLifetime
{
    private readonly CustomWebApplicationFactory _factory;
    private readonly string _a = "a-" + Guid.NewGuid().ToString("N");
    private readonly string _b = "b-" + Guid.NewGuid().ToString("N");
    private readonly byte[] _seed = RandomNumberGenerator.GetBytes(32);
    private readonly string _location = "loc-" + Guid.NewGuid().ToString("N");
    private HttpClient _client = null!;

    public NodeRequestSecurityTests(CustomWebApplicationFactory factory) => _factory = factory;

    public async Task InitializeAsync()
    {
        _client = _factory.CreateClient();
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        using var key = Key.Import(SignatureAlgorithm.Ed25519, _seed, KeyBlobFormat.RawPrivateKey);
        db.Locations.Add(new Location { LocationId = _location, Code = _location + "-alias", Country = "FI", City = "Helsinki", DisplayName = "FI", Enabled = true });
        foreach (var id in new[] { _a, _b })
        {
            db.Nodes.Add(new Node { NodeId = id, LocationId = _location, PublicIdentity = key.Export(KeyBlobFormat.RawPublicKey), DisplayName = id, Enabled = true, Capacity = 100, ConfigVersion = 5 });
            db.NodeCredentials.Add(new NodeCredential { NodeId = id, PublicKey = key.Export(KeyBlobFormat.RawPublicKey) });
            db.NodeConfigs.Add(new NodeConfig { NodeId = id, Enabled = true, Capacity = 100, ConfigVersion = 5 });
            db.NodeHealth.Add(new NodeHealth { NodeId = id });
        }
        await db.SaveChangesAsync();
    }

    public Task DisposeAsync() { _client.Dispose(); return Task.CompletedTask; }

    private string Body(string? id = null, int sessions = 3) => JsonSerializer.Serialize(new { node_id = id ?? _a, current_sessions = sessions });
    private static string B64(byte[] b) => Convert.ToBase64String(b).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    private HttpRequestMessage Signed(string method, string path, string body = "", string? nonce = null, long? timestamp = null,
        string? signedMethod = null, string? signedPath = null, string? signedBody = null)
    {
        var unix = (timestamp ?? DateTimeOffset.UtcNow.ToUnixTimeSeconds()).ToString(System.Globalization.CultureInfo.InvariantCulture);
        nonce ??= B64(RandomNumberGenerator.GetBytes(16));
        var hash = Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(signedBody ?? body))).ToLowerInvariant();
        var message = Encoding.UTF8.GetBytes($"nvp-node-req-v2|{_a}|{unix}|{nonce}|{signedMethod ?? method}|{signedPath ?? path}|{hash}");
        using var key = Key.Import(SignatureAlgorithm.Ed25519, _seed, KeyBlobFormat.RawPrivateKey);
        var req = new HttpRequestMessage(new HttpMethod(method), path);
        req.Headers.Add("X-Node-Id", _a);
        req.Headers.Add("X-Node-Timestamp", unix);
        req.Headers.Add("X-Node-Nonce", nonce);
        req.Headers.Add("X-Node-Signature", B64(SignatureAlgorithm.Ed25519.Sign(key, message)));
        if (body.Length > 0) req.Content = new StringContent(body, Encoding.UTF8, "application/json");
        return req;
    }

    private async Task<HttpStatusCode> Send(HttpRequestMessage request)
    {
        using (request)
        using (var response = await _client.SendAsync(request)) return response.StatusCode;
    }

    [Fact]
    public async Task TestNodeACannotUpdateNodeBHealth()
    {
        Assert.Equal(HttpStatusCode.Forbidden, await Send(Signed("POST", $"/api/v1/nodes/{_b}/health", Body(_b, 99))));
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var b = await db.Nodes.AsNoTracking().SingleAsync(n => n.NodeId == _b);
        Assert.Equal(0, b.CurrentSessions);
        Assert.Null(b.LastSeenAt);
        Assert.Equal(5, b.ConfigVersion);
        Assert.Equal(0, (await db.NodeHealth.SingleAsync(n => n.NodeId == _b)).ActiveSessions);
        Assert.False(await db.NodeMetrics.AnyAsync(n => n.NodeId == _b));
    }

    [Fact]
    public async Task TestRouteNodeIdMustMatchAuthenticatedNode() =>
        Assert.Equal(HttpStatusCode.Forbidden, await Send(Signed("POST", $"/api/v1/nodes/{_b}/health", Body())));
    [Fact]
    public async Task TestBodyNodeIdMustMatchAuthenticatedNode() =>
        Assert.Equal(HttpStatusCode.Forbidden, await Send(Signed("POST", "/api/v1/node/heartbeat", Body(_b))));
    [Fact]
    public async Task TestQueryNodeIdMustMatchAuthenticatedNode() =>
        Assert.Equal(HttpStatusCode.Forbidden, await Send(Signed("GET", $"/api/v1/node/config?node_id={_b}")));
    [Fact]
    public async Task TestDuplicateBodyIdentityRejected() =>
        Assert.Equal(HttpStatusCode.Forbidden, await Send(Signed("POST", "/api/v1/node/heartbeat", $"{{\"node_id\":\"{_b}\",\"node_id\":\"{_a}\"}}")));
    [Fact]
    public async Task TestDuplicateQueryIdentityRejected() =>
        Assert.Equal(HttpStatusCode.Forbidden, await Send(Signed("GET", $"/api/v1/node/config?node_id={_a}&NodeId={_b}")));

    [Fact]
    public async Task TestSignedRequestUsesActualHttpMethod()
    {
        var request = Signed("POST", "/api/v1/node/heartbeat", Body(), signedMethod: "GET");
        request.Headers.Add("X-Node-Method", "GET");
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(request));
    }

    [Fact]
    public async Task TestSignedRequestUsesActualPath()
    {
        var request = Signed("POST", "/api/v1/node/heartbeat", Body(), signedPath: "/api/v1/node/config");
        request.Headers.Add("X-Node-Path", "/api/v1/node/config");
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(request));
    }

    [Fact]
    public async Task TestModifiedQueryRejected() =>
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("GET", "/api/v1/node/config?v=2", signedPath: "/api/v1/node/config?v=1")));
    [Fact]
    public async Task TestModifiedBodyRejected() =>
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("POST", "/api/v1/node/heartbeat", Body(sessions: 99), signedBody: Body(sessions: 1))));
    [Fact]
    public async Task TestExpiredSignedRequestRejected() =>
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("GET", "/api/v1/node/config", timestamp: DateTimeOffset.UtcNow.ToUnixTimeSeconds() - 301)));
    [Fact]
    public async Task TestFutureSignedRequestRejected() =>
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("GET", "/api/v1/node/config", timestamp: DateTimeOffset.UtcNow.ToUnixTimeSeconds() + 310)));
    [Fact]
    public async Task TestOutOfRangeTimestampRejected() =>
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("GET", "/api/v1/node/config", timestamp: long.MaxValue)));
    [Fact]
    public async Task TestShortNonceRejected() =>
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("GET", "/api/v1/node/config", nonce: B64(new byte[8]))));
    [Fact]
    public async Task TestBodyLimitEnforced() =>
        Assert.Equal(HttpStatusCode.RequestEntityTooLarge, await Send(Signed("POST", "/api/v1/node/heartbeat", "{\"padding\":\"" + new string('a', 65536) + "\"}")));
    [Fact]
    public async Task TestSignedHeartbeatAccepted() =>
        Assert.Equal(HttpStatusCode.OK, await Send(Signed("POST", $"/api/v1/nodes/{_a}/health", Body())));
    [Fact]
    public async Task TestSignedGetConfigAccepted() =>
        Assert.Equal(HttpStatusCode.OK, await Send(Signed("GET", $"/api/v1/node/config?node_id={_a}&x=a%2Fb&x=a+b")));
    [Fact]
    public async Task TestSignedRevocationAccepted() =>
        Assert.Equal(HttpStatusCode.OK, await Send(Signed("GET", "/api/v1/revocation")));

    [Fact]
    public async Task TestCoreTokenRejectedForNormalNodeApi()
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, "/api/v1/node/config");
        request.Headers.Add("X-Node-Id", _a);
        request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", CoreNodeToken.Sign(_a, _seed, DateTime.UtcNow));
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(request));
    }

    [Fact]
    public async Task TestReplayNonceRejected()
    {
        var nonce = B64(RandomNumberGenerator.GetBytes(16));
        Assert.Equal(HttpStatusCode.OK, await Send(Signed("GET", "/api/v1/node/config", nonce: nonce)));
        Assert.Equal(HttpStatusCode.Unauthorized, await Send(Signed("GET", "/api/v1/node/config", nonce: nonce)));
    }

    [Fact]
    public async Task TestConcurrentReplayOnlyOneAccepted()
    {
        var nonce = B64(RandomNumberGenerator.GetBytes(16));
        var results = await Task.WhenAll(Enumerable.Range(0, 8).Select(_ => Send(Signed("POST", "/api/v1/node/heartbeat", Body(), nonce))));
        Assert.Single(results, s => s == HttpStatusCode.OK);
        Assert.Equal(7, results.Count(s => s == HttpStatusCode.Unauthorized));
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        Assert.Equal(1, await db.NodeMetrics.CountAsync(n => n.NodeId == _a));
        Assert.Equal(1, await db.NodeRequestNonces.CountAsync(n => n.NodeId == _a));
        Assert.NotNull((await db.NodeCredentials.SingleAsync(n => n.NodeId == _a)).LastAuthAt);
    }

    [Fact]
    public async Task TestHeartbeatThenImmediateConfigPullSucceeds()
    {
        var ts = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        Assert.Equal(HttpStatusCode.OK, await Send(Signed("POST", "/api/v1/node/heartbeat", Body(), timestamp: ts)));
        Assert.Equal(HttpStatusCode.OK, await Send(Signed("GET", "/api/v1/node/config", timestamp: ts)));
    }

    private async Task<NodeConfigResponse> PullConfig()
    {
        using var request = Signed("GET", "/api/v1/node/config");
        using var response = await _client.SendAsync(request);
        response.EnsureSuccessStatusCode();
        var json = await response.Content.ReadAsStringAsync();
        Assert.Contains("\"location_id\"", json);
        return JsonSerializer.Deserialize<NodeConfigResponse>(json)!;
    }

    [Fact] public async Task TestNodeConfigContainsCanonicalLocationId() => Assert.Equal(_location, (await PullConfig()).LocationId);

    private async Task<string> ChangeLocation()
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var target = "de-" + Guid.NewGuid().ToString("N");
        db.Locations.Add(new Location { LocationId = target, Code = target + "-alias", DisplayName = "Germany", Country = "DE", City = "Berlin", Enabled = true });
        await db.SaveChangesAsync();
        await scope.ServiceProvider.GetRequiredService<INodeManagementService>().ChangeLocationAsync(_a, target + "-alias", "admin");
        return target;
    }

    [Fact] public async Task TestAdminLocationChangeAppearsInNodeConfig() => Assert.Equal(await ChangeLocation(), (await PullConfig()).LocationId);
    [Fact] public async Task TestLocationChangeIncrementsConfigVersion() { await ChangeLocation(); Assert.Equal(6, (await PullConfig()).ConfigVersion); }

    [Fact]
    public async Task TestHeartbeatThenConfigPullReturnsNewLocation()
    {
        var target = await ChangeLocation();
        using var request = Signed("POST", "/api/v1/node/heartbeat", Body());
        using var response = await _client.SendAsync(request);
        response.EnsureSuccessStatusCode();
        var heartbeat = await response.Content.ReadFromJsonAsync<NodeHeartbeatResponse>();
        Assert.Equal(6, heartbeat!.ConfigVersion);
        var config = await PullConfig();
        Assert.Equal(target, config.LocationId);
        Assert.Equal(heartbeat.ConfigVersion, config.ConfigVersion);
    }

    [Fact]
    public async Task TestRegistrationConfigContainsLocationId()
    {
        using var scope = _factory.Services.CreateScope();
        var bootstrap = await scope.ServiceProvider.GetRequiredService<IBootstrapTokenService>().CreateAsync(new CreateBootstrapTokenRequest
        { AllowedLocation = _location, ExpiresAt = DateTime.UtcNow.AddHours(1), MaxUses = 1 });
        using var key = Key.Import(SignatureAlgorithm.Ed25519, _seed, KeyBlobFormat.RawPrivateKey);
        using var response = await _client.PostAsJsonAsync("/api/v1/nodes/register", new NodeRegisterRequest
        {
            NodeId = "new-" + Guid.NewGuid().ToString("N"),
            LocationId = _location,
            BootstrapToken = bootstrap.BootstrapToken,
            PublicIdentity = key.Export(KeyBlobFormat.RawPublicKey),
            PublicKey = key.Export(KeyBlobFormat.RawPublicKey)
        });
        response.EnsureSuccessStatusCode();
        var registered = await response.Content.ReadFromJsonAsync<NodeRegisterResponse>();
        Assert.Equal(_location, registered!.Config!.LocationId);
        Assert.Empty(registered.NodeToken);
    }

    [Fact]
    public async Task TestConcurrentAdminChangesConflictThenRetryPreservesBoth()
    {
        using var first = _factory.Services.CreateScope();
        using var second = _factory.Services.CreateScope();
        foreach (var scope in new[] { first, second })
        {
            var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
            await db.Nodes.SingleAsync(n => n.NodeId == _a);
            Assert.Equal(5, (await db.NodeConfigs.SingleAsync(n => n.NodeId == _a)).ConfigVersion);
        }
        var m1 = first.ServiceProvider.GetRequiredService<INodeManagementService>();
        var m2 = second.ServiceProvider.GetRequiredService<INodeManagementService>();
        var results = await Task.WhenAll(
            Record.ExceptionAsync(() => m1.SetDrainingAsync(_a, true, "admin-one")),
            Record.ExceptionAsync(() => m2.SetCapacityAsync(_a, 20, "admin-two")));
        Assert.Single(results, e => e is null);
        Assert.Single(results, e => e is ConflictException);
        if (results[0] is not null) await m1.SetDrainingAsync(_a, true, "admin-one");
        else await m2.SetCapacityAsync(_a, 20, "admin-two");
        var cfg = await PullConfig();
        Assert.Equal(7, cfg.ConfigVersion);
        Assert.True(cfg.Draining);
        Assert.Equal(20, cfg.Capacity);
        using var check = _factory.Services.CreateScope();
        var final = check.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        Assert.Equal(7, (await final.Nodes.SingleAsync(n => n.NodeId == _a)).ConfigVersion);
        Assert.Equal(2, await final.AuditLog.CountAsync(a => a.EntityId == _a));
    }

    [Fact]
    public async Task TestAuditFailureRollsBackConfigAndProjection()
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var service = new NodeManagementService(db, scope.ServiceProvider.GetRequiredService<IClock>(), new FailingAudit(db));
        await Assert.ThrowsAsync<DbUpdateException>(() => service.SetDrainingAsync(_a, true, "admin"));
        var cfg = await PullConfig();
        Assert.Equal(5, cfg.ConfigVersion);
        Assert.False(cfg.Draining);
        using var check = _factory.Services.CreateScope();
        var final = check.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var node = await final.Nodes.SingleAsync(n => n.NodeId == _a);
        Assert.Equal(5, node.ConfigVersion);
        Assert.False(node.Draining);
        Assert.False(await final.AuditLog.AnyAsync(a => a.EntityId == _a));
    }

    private sealed class FailingAudit(ControlPlaneDbContext db) : IAuditService
    {
        public async Task WriteAsync(AuditWriteRequest request, CancellationToken cancellationToken = default)
        {
            // Force a real database NOT NULL failure after the config SaveChanges.
            db.AuditLog.Add(new AuditLogEntry { Id = Guid.NewGuid(), Actor = null!, Action = request.Action, EntityType = "Node", EntityId = request.EntityId });
            await db.SaveChangesAsync(cancellationToken);
        }
    }
}
