using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Infrastructure.Security;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

public sealed class NodeRegistrationService : INodeRegistrationService
{
    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly NodeAuthService _nodeAuth;
    private readonly IClock _clock;
    private readonly NodeAuthOptions _nodeAuthOptions;

    public NodeRegistrationService(
        ControlPlaneDbContext db,
        ILicenseKeyHasher hasher,
        NodeAuthService nodeAuth,
        IClock clock,
        IOptions<NodeAuthOptions> nodeAuthOptions)
    {
        _db = db;
        _hasher = hasher;
        _nodeAuth = nodeAuth;
        _clock = clock;
        _nodeAuthOptions = nodeAuthOptions.Value;
    }

    public async Task<NodeRegisterResponse> RegisterWithBootstrapAsync(
        NodeRegisterRequest request,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(request.NodeId))
            throw new ValidationException("node_id is required");
        if (string.IsNullOrWhiteSpace(request.LocationId))
            throw new ValidationException("location_id is required");
        if (request.PublicIdentity is not { Length: 32 })
            throw new ValidationException("public_identity must be 32 bytes");
        if (request.PublicKey is not { Length: 32 })
            throw new ValidationException("public_key must be 32 bytes");

        var location = await _db.Locations
            .FirstOrDefaultAsync(l => l.LocationId == request.LocationId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("location not found");

        var existing = await _db.Nodes
            .FirstOrDefaultAsync(n => n.NodeId == request.NodeId, cancellationToken)
            .ConfigureAwait(false);

        if (existing is not null)
        {
            if (!existing.PublicIdentity.SequenceEqual(request.PublicIdentity))
                throw new ConflictException("node_id already registered with different identity");

            // Existing node: require PoP of registered credential key. No bootstrap reset,
            // no public key replace, no new bearer secret.
            if (string.IsNullOrWhiteSpace(request.NodeToken))
                throw new ForbiddenException("existing node requires proof-of-possession");

            await _nodeAuth.ValidateNodeRequestAsync(
                    existing.NodeId,
                    new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
                    {
                        ["Authorization"] = "Bearer " + request.NodeToken.Trim()
                    },
                    cancellationToken)
                .ConfigureAwait(false);

            var cred = await _db.NodeCredentials.AsNoTracking()
                .FirstOrDefaultAsync(c => c.NodeId == existing.NodeId, cancellationToken)
                .ConfigureAwait(false);
            if (cred is not null && !cred.PublicKey.AsSpan().SequenceEqual(request.PublicKey))
                throw new ForbiddenException("node public key cannot be replaced via registration");

            var cfg = await GetConfigAsync(existing.NodeId, cancellationToken).ConfigureAwait(false);
            return new NodeRegisterResponse
            {
                NodeId = existing.NodeId,
                Registered = true,
                NodeToken = string.Empty,
                ConfigVersion = existing.ConfigVersion,
                Config = cfg
            };
        }

        await using var tx = await _db.Database.BeginTransactionAsync(cancellationToken).ConfigureAwait(false);

        await FindAndConsumeBootstrapAsync(request.BootstrapToken, location, cancellationToken)
            .ConfigureAwait(false);

        var now = _clock.UtcNow;
        var node = new Node
        {
            NodeId = request.NodeId,
            LocationId = location.LocationId,
            DisplayName = string.IsNullOrWhiteSpace(request.DisplayName) ? request.NodeId : request.DisplayName,
            Status = NodeRuntimeStatus.Offline,
            Enabled = true,
            TestOnly = request.TestOnly,
            ProtocolVersion = request.ProtocolVersion,
            ServerVersion = request.ServerVersion,
            ServerName = request.ServerName,
            SpkiPin = request.SpkiPin,
            PublicIdentity = request.PublicIdentity,
            Capacity = request.Capacity > 0 ? request.Capacity : 100,
            CurrentSessions = 0,
            CreatedAt = now,
            UpdatedAt = now,
            ConfigVersion = 1
        };

        foreach (var ep in request.Endpoints)
        {
            node.Endpoints.Add(new NodeEndpoint
            {
                Id = Guid.NewGuid(),
                NodeId = node.NodeId,
                Host = ep.Host,
                Port = ep.Port,
                AddressFamily = string.IsNullOrWhiteSpace(ep.AddressFamily) ? "hostname" : ep.AddressFamily,
                Priority = ep.Priority,
                Enabled = ep.Enabled
            });
        }

        // Default production transports for catalog profiles.
        node.Transports.Add(new NodeTransport
        {
            Id = Guid.NewGuid(),
            NodeId = node.NodeId,
            TransportType = "quic",
            Enabled = true,
            Priority = 1
        });
        node.Transports.Add(new NodeTransport
        {
            Id = Guid.NewGuid(),
            NodeId = node.NodeId,
            TransportType = "tls",
            Enabled = true,
            Priority = 2
        });

        string? bearer = null;
        string? verifier = null;
        if (_nodeAuthOptions.AllowLegacyBearer)
        {
            bearer = _nodeAuth.IssueNodeBearerToken(node.NodeId);
            verifier = _nodeAuth.CreateNodeBearerVerifier(node.NodeId, bearer);
        }

        _db.Nodes.Add(node);
        _db.NodeCredentials.Add(new NodeCredential
        {
            NodeId = node.NodeId,
            PublicKey = request.PublicKey,
            CredentialIssuedAt = now,
            NodeAuthSecretVerifier = verifier
        });
        _db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = node.NodeId,
            Enabled = true,
            Capacity = node.Capacity,
            TransportPolicyJson = "{}",
            ConfigVersion = 1,
            UpdatedAt = now
        });
        _db.NodeHealth.Add(new NodeHealth
        {
            NodeId = node.NodeId,
            UpdatedAt = now
        });

        // Bootstrap already atomically consumed in FindAndConsumeBootstrapAsync.

        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
        await tx.CommitAsync(cancellationToken).ConfigureAwait(false);

        return new NodeRegisterResponse
        {
            NodeId = node.NodeId,
            Registered = true,
            NodeToken = bearer ?? string.Empty,
            ConfigVersion = node.ConfigVersion,
            Config = await GetConfigAsync(node.NodeId, cancellationToken).ConfigureAwait(false)
        };
    }

    public async Task<NodeConfigResponse> GetConfigAsync(string nodeId, CancellationToken cancellationToken = default)
    {
        var cfg = await _db.NodeConfigs.AsNoTracking()
            .FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new NotFoundException("node config not found");

        return new NodeConfigResponse
        {
            NodeId = cfg.NodeId,
            Enabled = cfg.Enabled,
            Draining = cfg.Draining,
            MaintenanceMode = cfg.MaintenanceMode,
            TransportPolicyJson = cfg.TransportPolicyJson,
            EchPolicyJson = cfg.EchPolicyJson,
            Mtu = cfg.Mtu,
            Capacity = cfg.Capacity,
            MinimumServerVersion = cfg.MinimumServerVersion,
            MinimumProtocolVersion = cfg.MinimumProtocolVersion,
            ConfigVersion = cfg.ConfigVersion,
            UpdatedAt = cfg.UpdatedAt
        };
    }

    /// <summary>
    /// Atomically consumes one use: UPDATE ... WHERE UsedCount &lt; MaxUses (and Active / not expired).
    /// </summary>
    private async Task FindAndConsumeBootstrapAsync(
        string rawToken,
        Location location,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(rawToken))
            throw new UnauthorizedException("bootstrap_token required");

        var candidates = await _db.BootstrapTokens
            .Where(t => t.Status == BootstrapTokenStatus.Active)
            .ToListAsync(cancellationToken)
            .ConfigureAwait(false);

        BootstrapToken? match = null;
        foreach (var t in candidates)
        {
            if (_hasher.Verify(t.Verifier, rawToken))
            {
                match = t;
                break;
            }
        }

        if (match is null)
            throw new UnauthorizedException("invalid bootstrap token");

        if (match.ExpiresAt <= _clock.UtcNow)
        {
            match.Status = BootstrapTokenStatus.Expired;
            await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
            throw new UnauthorizedException("bootstrap token expired");
        }

        if (!string.IsNullOrEmpty(match.AllowedLocation) &&
            !string.Equals(match.AllowedLocation, location.LocationId, StringComparison.Ordinal) &&
            !string.Equals(match.AllowedLocation, location.Code, StringComparison.Ordinal))
            throw new ForbiddenException("bootstrap token location mismatch");

        var now = _clock.UtcNow;
        var id = match.BootstrapId;
        var maxUses = match.MaxUses;

        if (_db.Database.IsSqlServer())
        {
            var rows = await _db.Database.ExecuteSqlInterpolatedAsync(
                    $"""
                     UPDATE BootstrapTokens
                     SET UsedCount = UsedCount + 1,
                         Status = CASE WHEN UsedCount + 1 >= MaxUses THEN {(int)BootstrapTokenStatus.Exhausted} ELSE Status END
                     WHERE BootstrapId = {id}
                       AND Status = {(int)BootstrapTokenStatus.Active}
                       AND UsedCount < MaxUses
                       AND ExpiresAt > {now};
                     """,
                    cancellationToken)
                .ConfigureAwait(false);
            if (rows != 1)
                throw new UnauthorizedException("bootstrap token exhausted");
            return;
        }

        // InMemory / providers without ExecuteUpdate: emulate atomic consume under the ambient transaction.
        // Prefer MSSQL path above in production. Concurrent InMemory races are not fully covered.
        if (match.UsedCount >= match.MaxUses || match.Status != BootstrapTokenStatus.Active || match.ExpiresAt <= now)
            throw new UnauthorizedException("bootstrap token exhausted");

        match.UsedCount += 1;
        if (match.UsedCount >= maxUses)
            match.Status = BootstrapTokenStatus.Exhausted;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }
}
