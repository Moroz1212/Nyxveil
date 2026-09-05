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
    private static readonly TimeSpan MaxSkew = TimeSpan.FromMinutes(5);

    private readonly ControlPlaneDbContext _db;
    private readonly ILicenseKeyHasher _hasher;
    private readonly IClock _clock;

    public NodeAuthService(
        ControlPlaneDbContext db,
        ILicenseKeyHasher hasher,
        IClock clock,
        IOptions<NodeAuthOptions> options)
    {
        _db = db;
        _hasher = hasher;
        _clock = clock;
        _ = options;
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

        if (cred.NodeId != nodeId) throw new UnauthorizedException("node identity mismatch");
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
        NodeRequestData request,
        CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(nodeId) || nodeId.Length > 128 ||
            nodeId.Any(c => !char.IsAsciiLetterOrDigit(c) && c is not '-' and not '_' and not '.'))
            throw new UnauthorizedException("invalid node id");
        var headers = new Dictionary<string, string>(signatureHeaders, StringComparer.OrdinalIgnoreCase);
        if (!headers.TryGetValue("X-Node-Signature", out var signature) ||
            !headers.TryGetValue("X-Node-Timestamp", out var timestamp) ||
            !headers.TryGetValue("X-Node-Nonce", out var nonce))
            throw new UnauthorizedException("req-v2 signature required");
        if (!long.TryParse(timestamp, System.Globalization.NumberStyles.None,
                System.Globalization.CultureInfo.InvariantCulture, out var unix) ||
            timestamp != unix.ToString(System.Globalization.CultureInfo.InvariantCulture))
            throw new UnauthorizedException("invalid timestamp");
        var now = _clock.UtcNow;
        DateTime ts;
        try { ts = DateTimeOffset.FromUnixTimeSeconds(unix).UtcDateTime; }
        catch (ArgumentOutOfRangeException) { throw new UnauthorizedException("invalid timestamp"); }
        if (now - ts > MaxSkew || ts - now > MaxSkew)
            throw new UnauthorizedException("node signature expired");
        byte[] nonceBytes;
        byte[] sig;
        try
        {
            nonceBytes = Base64UrlDecode(nonce);
            sig = Base64UrlDecode(signature);
        }
        catch (FormatException) { throw new UnauthorizedException("invalid signature encoding"); }
        if (nonceBytes.Length is < 16 or > 64 || nonce != Encode(nonceBytes) || sig.Length != 64 || signature != Encode(sig))
            throw new UnauthorizedException("invalid nonce or signature");
        var cred = await _db.NodeCredentials.AsNoTracking().SingleOrDefaultAsync(c => c.NodeId == nodeId, cancellationToken)
            ?? throw new UnauthorizedException("unknown node");
        // SQL installations may use case-insensitive collation. Identity remains ordinal.
        if (cred.NodeId != nodeId || cred.PublicKey.Length != 32)
            throw new UnauthorizedException("invalid node identity");
        var msg = Encoding.UTF8.GetBytes($"nvp-node-req-v2|{nodeId}|{timestamp}|{nonce}|{request.Method}|{request.PathAndQuery}|{request.BodySha256}");
        var pub = PublicKey.Import(SignatureAlgorithm.Ed25519, cred.PublicKey, KeyBlobFormat.RawPublicKey);
        if (!SignatureAlgorithm.Ed25519.Verify(pub, msg, sig))
            throw new UnauthorizedException("invalid node signature");
        var nonceHash = Convert.ToHexString(SHA256.HashData(nonceBytes)).ToLowerInvariant();
        // The composite PK arbitrates concurrent requests across all service instances.
        if (await _db.NodeRequestNonces.AnyAsync(n => n.NodeId == nodeId && n.NonceHash == nonceHash, cancellationToken))
            throw new UnauthorizedException("node request replayed");
        var row = new Nyxveil.ControlPlane.Domain.Entities.NodeRequestNonce
        {
            NodeId = nodeId,
            NonceHash = nonceHash,
            Timestamp = ts,
            ExpiresAt = ts.Add(MaxSkew).AddSeconds(1)
        };
        var authenticatedCredential = await _db.NodeCredentials.SingleAsync(c => c.NodeId == nodeId, cancellationToken);
        authenticatedCredential.LastAuthAt = now;
        _db.NodeRequestNonces.Add(row);
        try { await _db.SaveChangesAsync(cancellationToken); }
        catch (DbUpdateException)
        {
            _db.Entry(row).State = EntityState.Detached;
            if (await _db.NodeRequestNonces.AnyAsync(n => n.NodeId == nodeId && n.NonceHash == nonceHash, cancellationToken))
                throw new UnauthorizedException("node request replayed");
            throw;
        }
    }

    private static string Encode(byte[] bytes) => Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');

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
        DateTime ts;
        try { ts = DateTimeOffset.FromUnixTimeSeconds(tsUnix).UtcDateTime; }
        catch (ArgumentOutOfRangeException) { throw new UnauthorizedException("invalid node token"); }
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
