using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

/// <summary>
/// NVP/1 access ticket claims (core/auth/ticket.Claims) plus JWT registered fields
/// used when issuing tickets.
/// </summary>
public sealed class AccessTicketClaims
{
    [JsonPropertyName("jti")]
    public string? Jti { get; set; }

    [JsonPropertyName("iss")]
    public string? Issuer { get; set; }

    [JsonPropertyName("aud")]
    public IReadOnlyList<string>? Audience { get; set; }

    [JsonPropertyName("iat")]
    public long? IssuedAt { get; set; }

    [JsonPropertyName("nbf")]
    public long? NotBefore { get; set; }

    [JsonPropertyName("exp")]
    public long? ExpiresAt { get; set; }

    [JsonPropertyName("license_id")]
    public string LicenseId { get; set; } = string.Empty;

    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;

    [JsonPropertyName("role")]
    public string Role { get; set; } = string.Empty;

    [JsonPropertyName("plan")]
    public string Plan { get; set; } = string.Empty;

    [JsonPropertyName("permissions")]
    public IReadOnlyList<string> Permissions { get; set; } = Array.Empty<string>();

    [JsonPropertyName("locations")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public IReadOnlyList<string>? Locations { get; set; }

    [JsonPropertyName("node_scope")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public IReadOnlyList<string>? NodeScope { get; set; }

    [JsonPropertyName("protocol_version")]
    public string ProtocolVersion { get; set; } = "NVP/1";

    [JsonPropertyName("device_pub")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public byte[]? DevicePub { get; set; }

    public bool HasPermission(string permission)
    {
        foreach (var p in Permissions)
        {
            if (string.Equals(p, permission, StringComparison.Ordinal))
                return true;
        }

        return false;
    }
}
