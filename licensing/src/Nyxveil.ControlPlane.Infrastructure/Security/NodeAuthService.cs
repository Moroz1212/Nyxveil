using System.Security.Cryptography;
using System.Text;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;
using NSec.Cryptography;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

public sealed class NodeAuthService : INodeAuthenticator
{
    private const string SigPrefix = "nvp-node-req-v1";
    private static readonly TimeSpan MaxSkew = TimeSpan.FromMinutes(5);

    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly IClock _clock;
    private readonly NodeAuthOptions _options;

    public NodeAuthService(
        ControlPlaneDbContext db,
        ILicenseKeyHasher hasher,
        IClock clock,
        IOptions<NodeAuthOptions> options)
    {
        _db = db;
        _hasher = hasher;
        _clock = clock;
        _options = options.Value;
    }

    public string IssueNodeBearerToken(string nodeId)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new ValidationException("node_id is required");

        var raw = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();
        return $"nvpnode_{nodeId}_{raw}";
    }

    public string CreateNodeBearerVerifier(string nodeId, string rawToken)
    {
        var material = nodeId + "\n" + rawToken;
        return _hasher.CreateVerifier(material);
    }

    public static string SignCoreNodeTokenV1(string nodeId, byte[] privateKeySeed, long unixSeconds)
    {
        var utc = DateTimeOffset.FromUnixTimeSeconds(unixSeconds).UtcDateTime;
        return CoreNodeToken.Sign(nodeId, privateKeySeed, utc);
    }

    public async Task VerifyCoreNodeTokenV1Async(
        string nodeId,
        string token,
        CancellationToken cancellationToken = default)
    {
        var cred = await _db.NodeCredentials
            .AsNoTracking()
            .FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new UnauthorizedException("unknown node");

        var tokenUnix = CoreNodeToken.Verify(nodeId, token.Trim(), cred.PublicKey, _clock.UtcNow);
        await AcceptCoreTokenUnixAsync(nodeId, tokenUnix, cancellationToken).ConfigureAwait(false);

        var tracked = await _db.NodeCredentials.FirstAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        tracked.LastAuthAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task ValidateNodeRequestAsync(
        string nodeId,
        IReadOnlyDictionary<string, string> signatureHeaders,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
            throw new UnauthorizedException("missing node id");

        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (signatureHeaders is not null)
        {
            foreach (var kv in signatureHeaders)
                headers[kv.Key] = kv.Value;
        }

        var cred = await _db.NodeCredentials
            .AsNoTracking()
            .FirstOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false)
            ?? throw new UnauthorizedException("unknown node");

        var bearer = ExtractBearer(headers);
        var authenticated = false;

        if (!string.IsNullOrWhiteSpace(bearer))
        {
            if (CoreNodeToken.LooksLike(bearer))
            {
                var tokenUnix = CoreNodeToken.Verify(nodeId, bearer.Trim(), cred.PublicKey, _clock.UtcNow);
                await AcceptCoreTokenUnixAsync(nodeId, tokenUnix, cancellationToken).ConfigureAwait(false);
                authenticated = true;
            }
            else if (_options.AllowLegacyBearer)
            {
                if (string.IsNullOrEmpty(cred.NodeAuthSecretVerifier))
                    throw new UnauthorizedException("node bearer not configured");

                var material = nodeId + "\n" + bearer.Trim();
                if (!_hasher.Verify(cred.NodeAuthSecretVerifier, material))
                    throw new UnauthorizedException("invalid node token");

                if (!bearer.StartsWith("nvpnode_" + nodeId + "_", StringComparison.Ordinal))
                    throw new UnauthorizedException("node token binding mismatch");

                authenticated = true;
            }
            else if (bearer.StartsWith("nvpnode_", StringComparison.Ordinal))
            {
                throw new UnauthorizedException("legacy node bearer disabled");
            }
        }

        headers.TryGetValue("X-Node-Signature", out var signatureBase64);
        headers.TryGetValue("X-Node-Timestamp", out var timestamp);
        headers.TryGetValue("X-Node-Method", out var method);
        headers.TryGetValue("X-Node-Path", out var path);
        if (string.IsNullOrEmpty(method))
            headers.TryGetValue(":method", out method);
        if (string.IsNullOrEmpty(path))
            headers.TryGetValue(":path", out path);

        if (!string.IsNullOrWhiteSpace(signatureBase64))
        {
            if (string.IsNullOrWhiteSpace(timestamp) || string.IsNullOrWhiteSpace(method) || string.IsNullOrWhiteSpace(path))
                throw new UnauthorizedException("incomplete signature headers");

            if (!long.TryParse(timestamp, out var tsUnix))
                throw new UnauthorizedException("invalid timestamp");

            var ts = DateTimeOffset.FromUnixTimeSeconds(tsUnix).UtcDateTime;
            var now = _clock.UtcNow;
            if (now - ts > MaxSkew || ts - now > MaxSkew)
                throw new UnauthorizedException("node signature expired");

            byte[] sig;
            try { sig = Base64UrlDecode(signatureBase64); }
            catch (FormatException) { throw new UnauthorizedException("invalid signature encoding"); }

            var msg = Encoding.UTF8.GetBytes($"{SigPrefix}|{nodeId}|{timestamp}|{method.ToUpperInvariant()}|{path}");
            if (cred.PublicKey.Length != 32)
                throw new UnauthorizedException("invalid node public key");

            var pub = PublicKey.Import(SignatureAlgorithm.Ed25519, cred.PublicKey, KeyBlobFormat.RawPublicKey);
            if (!SignatureAlgorithm.Ed25519.Verify(pub, msg, sig))
                throw new UnauthorizedException("invalid node signature");

            authenticated = true;
        }

        if (!authenticated)
            throw new UnauthorizedException("node authentication required");

        var tracked = await _db.NodeCredentials.FirstAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        tracked.LastAuthAt = _clock.UtcNow;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    public async Task AcceptCoreTokenUnixAsync(
        string nodeId,
        long tokenUnix,
        CancellationToken cancellationToken = default)
    {
        if (_db.Database.IsSqlServer())
        {
            var rows = await _db.Database.ExecuteSqlInterpolatedAsync(
                    $"""
                     UPDATE NodeCredentials
                     SET LastCoreTokenUnix = {tokenUnix}
                     WHERE NodeId = {nodeId}
                       AND (LastCoreTokenUnix IS NULL OR LastCoreTokenUnix < {tokenUnix});
                     """,
                    cancellationToken)
                .ConfigureAwait(false);
            if (rows != 1)
                throw new UnauthorizedException("node token replayed");
            return;
        }

        var tracked = await _db.NodeCredentials.FirstAsync(c => c.NodeId == nodeId, cancellationToken)
            .ConfigureAwait(false);
        if (tracked.LastCoreTokenUnix is long last && tokenUnix <= last)
            throw new UnauthorizedException("node token replayed");
        tracked.LastCoreTokenUnix = tokenUnix;
        await _db.SaveChangesAsync(cancellationToken).ConfigureAwait(false);
    }

    private static string? ExtractBearer(IReadOnlyDictionary<string, string> headers)
    {
        if (headers.TryGetValue("Authorization", out var auth) &&
            auth.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
            return auth["Bearer ".Length..].Trim();

        if (headers.TryGetValue("node_token", out var token))
            return token;

        headers.TryGetValue("X-Node-Token", out token);
        return token;
    }

    private static byte[] Base64UrlDecode(string input)
    {
        var s = input.Replace('-', '+').Replace('_', '/');
        switch (s.Length % 4)
        {
            case 2: s += "=="; break;
            case 3: s += "="; break;
        }
        return Convert.FromBase64String(s);
    }
}

public static class CoreNodeToken
{
    private const string TokenPrefix = "nvp-node-v1";
    private static readonly TimeSpan MaxSkew = TimeSpan.FromMinutes(5);

    public static bool LooksLike(string token)
    {
        if (string.IsNullOrWhiteSpace(token)) return false;
        if (token.StartsWith("nvpnode_", StringComparison.Ordinal)) return false;
        var dot = token.IndexOf('.');
        return dot > 0 && long.TryParse(token.AsSpan(0, dot), out _) && token.Length > dot + 1;
    }

    public static long Verify(string nodeId, string token, byte[] publicKey, DateTime utcNow)
    {
        var dot = token.IndexOf('.');
        if (dot <= 0 || publicKey.Length != 32)
            throw new UnauthorizedException("invalid node token");
        if (!long.TryParse(token.AsSpan(0, dot), out var tsUnix))
            throw new UnauthorizedException("invalid node token");
        var ts = DateTimeOffset.FromUnixTimeSeconds(tsUnix).UtcDateTime;
        if (utcNow - ts > MaxSkew || ts - utcNow > MaxSkew)
            throw new UnauthorizedException("node token expired");
        byte[] sig;
        try { sig = Base64UrlDecode(token[(dot + 1)..]); }
        catch (FormatException) { throw new UnauthorizedException("invalid node token"); }
        var msg = Encoding.UTF8.GetBytes($"{TokenPrefix}|{nodeId}|{tsUnix}");
        var pub = PublicKey.Import(SignatureAlgorithm.Ed25519, publicKey, KeyBlobFormat.RawPublicKey);
        if (!SignatureAlgorithm.Ed25519.Verify(pub, msg, sig))
            throw new UnauthorizedException("invalid node token");
        return tsUnix;
    }

    public static string Sign(string nodeId, byte[] privateKeySeedOr64, DateTime utcNow)
    {
        var seed = privateKeySeedOr64.Length == 64
            ? privateKeySeedOr64.AsSpan(0, 32).ToArray()
            : privateKeySeedOr64;
        if (seed.Length != 32)
            throw new ArgumentException("Ed25519 private key seed must be 32 bytes.", nameof(privateKeySeedOr64));
        var tsUnix = new DateTimeOffset(DateTime.SpecifyKind(utcNow, DateTimeKind.Utc)).ToUnixTimeSeconds();
        var msg = Encoding.UTF8.GetBytes($"{TokenPrefix}|{nodeId}|{tsUnix}");
        using var key = Key.Import(SignatureAlgorithm.Ed25519, seed, KeyBlobFormat.RawPrivateKey);
        var sig = SignatureAlgorithm.Ed25519.Sign(key, msg);
        return $"{tsUnix}.{Convert.ToBase64String(sig).TrimEnd('=').Replace('+', '-').Replace('/', '_')}";
    }

    private static byte[] Base64UrlDecode(string input)
    {
        var s = input.Replace('-', '+').Replace('_', '/');
        switch (s.Length % 4)
        {
            case 2: s += "=="; break;
            case 3: s += "="; break;
        }
        return Convert.FromBase64String(s);
    }
}
