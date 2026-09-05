using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Issues and verifies EdDSA (Ed25519) JWTs matching Go core/auth/ticket claims.
/// </summary>
public sealed class AccessTicketService : IAccessTicketIssuer
{
    private static readonly JsonSerializerOptions JwtJson = new()
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        PropertyNamingPolicy = null
    };

    private const int MaxIssuedAtSkewSeconds = 300;
    private const int MaxNotBeforeSkewSeconds = 300;

    private readonly ISigningKeyService _keys;
    private readonly SigningOptions _signing;
    private readonly TicketOptions _ticket;
    private readonly IClock _clock;

    public AccessTicketService(
        ISigningKeyService keys,
        IOptions<SigningOptions> signing,
        IOptions<TicketOptions> ticket,
        IClock clock)
    {
        _keys = keys;
        _signing = signing.Value;
        _ticket = ticket.Value;
        _clock = clock;
    }

    public async Task<string> IssueJwtAsync(IssueTicketCommand command, CancellationToken cancellationToken = default)
    {
        var material = await _keys.GetCurrentSigningMaterialAsync(cancellationToken).ConfigureAwait(false);
        var now = _clock.UtcNow;
        var ttl = TimeSpan.FromMinutes(_ticket.TtlMinutes <= 0 ? 15 : _ticket.TtlMinutes);
        var exp = now.Add(ttl);
        var jti = "tkt_" + Convert.ToHexString(RandomNumberGenerator.GetBytes(16)).ToLowerInvariant();

        var payload = new Dictionary<string, object?>
        {
            ["jti"] = jti,
            ["iss"] = _signing.Issuer,
            ["aud"] = new[] { _signing.Audience },
            ["iat"] = ToUnix(now),
            ["nbf"] = ToUnix(now),
            ["exp"] = ToUnix(exp),
            ["license_id"] = command.LicenseId,
            ["device_id"] = command.DeviceId,
            ["role"] = command.Role,
            ["plan"] = command.Plan,
            ["permissions"] = command.Permissions,
            ["protocol_version"] = "NVP/1"
        };

        if (command.Locations is { Count: > 0 })
            payload["locations"] = command.Locations;
        if (command.NodeScope is { Count: > 0 })
            payload["node_scope"] = command.NodeScope;
        if (command.DevicePublicKey is { Length: > 0 })
            payload["device_pub"] = command.DevicePublicKey;

        var header = new Dictionary<string, object>
        {
            ["alg"] = "EdDSA",
            ["typ"] = "JWT",
            ["kid"] = material.KeyId
        };

        var headerJson = JsonSerializer.SerializeToUtf8Bytes(header, JwtJson);
        var payloadJson = JsonSerializer.SerializeToUtf8Bytes(payload, JwtJson);
        var signingInput = Encoding.ASCII.GetBytes(
            Base64UrlEncode(headerJson) + "." + Base64UrlEncode(payloadJson));
        var signature = Ed25519SigningKeyStore.Sign(material.PrivateKey, signingInput);
        return Encoding.ASCII.GetString(signingInput) + "." + Base64UrlEncode(signature);
    }

    public AccessTicketClaims ParseJwt(string token) => VerifyAccessTicket(token);

    public AccessTicketClaims VerifyAccessTicket(string token)
    {
        var parts = token.Split('.');
        if (parts.Length != 3)
            throw new UnauthorizedAccessException("invalid access ticket");

        var headerBytes = Base64UrlDecode(parts[0]);
        using var headerDoc = JsonDocument.Parse(headerBytes);
        if (!headerDoc.RootElement.TryGetProperty("alg", out var algEl) ||
            !string.Equals(algEl.GetString(), "EdDSA", StringComparison.Ordinal))
            throw new UnauthorizedAccessException("unexpected signing algorithm");

        var kid = headerDoc.RootElement.TryGetProperty("kid", out var kidEl) ? kidEl.GetString() : null;
        if (string.IsNullOrEmpty(kid))
            throw new UnauthorizedAccessException("missing kid");

        var keys = _keys.GetVerificationKeysAsync().GetAwaiter().GetResult();
        var pub = keys.FirstOrDefault(k => k.KeyId == kid)?.PublicKey
                  ?? throw new UnauthorizedAccessException("unknown key id");

        var signingInput = Encoding.ASCII.GetBytes(parts[0] + "." + parts[1]);
        var signature = Base64UrlDecode(parts[2]);
        if (!Ed25519SigningKeyStore.Verify(pub, signingInput, signature))
            throw new UnauthorizedAccessException("invalid access ticket signature");

        var payloadBytes = Base64UrlDecode(parts[1]);
        var claims = JsonSerializer.Deserialize<AccessTicketClaims>(payloadBytes, JwtJson)
                     ?? throw new UnauthorizedAccessException("invalid access ticket");

        if (!string.Equals(claims.Issuer, _signing.Issuer, StringComparison.Ordinal))
            throw new UnauthorizedAccessException("wrong issuer");

        if (!AudienceContainsExact(claims.Audience, _signing.Audience))
            throw new UnauthorizedAccessException("wrong audience");

        if (string.IsNullOrWhiteSpace(claims.Jti))
            throw new UnauthorizedAccessException("missing jti");
        if (string.IsNullOrWhiteSpace(claims.LicenseId))
            throw new UnauthorizedAccessException("missing license_id");
        if (string.IsNullOrWhiteSpace(claims.DeviceId))
            throw new UnauthorizedAccessException("missing device_id");

        if (!string.Equals(claims.ProtocolVersion, "NVP/1", StringComparison.Ordinal))
            throw new UnauthorizedAccessException("invalid protocol_version");

        if (claims.Permissions is null)
            throw new UnauthorizedAccessException("missing permissions");

        if (claims.DevicePub is null || claims.DevicePub.Length != 32)
            throw new UnauthorizedAccessException("device_pub must be 32 bytes");

        var now = ToUnix(_clock.UtcNow);
        if (claims.ExpiresAt is not long exp || now >= exp)
            throw new UnauthorizedAccessException("ticket expired");
        if (claims.NotBefore is long nbf && nbf > now + MaxNotBeforeSkewSeconds)
            throw new UnauthorizedAccessException("ticket not yet valid");
        if (claims.IssuedAt is long iat && iat > now + MaxIssuedAtSkewSeconds)
            throw new UnauthorizedAccessException("invalid issued-at");

        return claims;
    }

    /// <summary>Returns JWT string plus parsed expiry/jti for audit callers.</summary>
    public async Task<(string Jwt, DateTime ExpiresAt, string TicketId)> IssueDetailedAsync(
        IssueTicketCommand command,
        CancellationToken cancellationToken = default)
    {
        var jwt = await IssueJwtAsync(command, cancellationToken).ConfigureAwait(false);
        var claims = JsonSerializer.Deserialize<AccessTicketClaims>(Base64UrlDecode(jwt.Split('.')[1]), JwtJson)!;
        var exp = DateTimeOffset.FromUnixTimeSeconds(claims.ExpiresAt ?? 0).UtcDateTime;
        return (jwt, exp, claims.Jti ?? string.Empty);
    }

    private static bool AudienceContainsExact(IReadOnlyList<string>? audience, string expected)
    {
        if (audience is null || audience.Count == 0)
            return false;
        foreach (var a in audience)
        {
            if (string.Equals(a, expected, StringComparison.Ordinal))
                return true;
        }

        return false;
    }

    private static long ToUnix(DateTime utc) =>
        new DateTimeOffset(DateTime.SpecifyKind(utc, DateTimeKind.Utc)).ToUnixTimeSeconds();

    public static string Base64UrlEncode(ReadOnlySpan<byte> data) =>
        Convert.ToBase64String(data).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    public static byte[] Base64UrlDecode(string input)
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
