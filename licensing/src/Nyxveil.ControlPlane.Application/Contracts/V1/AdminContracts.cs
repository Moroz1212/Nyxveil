using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

public sealed class CreateLicenseRequest
{
    [JsonPropertyName("plan_id")]
    public Guid PlanId { get; set; }

    [JsonPropertyName("user_id")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public Guid? UserId { get; set; }

    [JsonPropertyName("role")]
    public string Role { get; set; } = "user";

    [JsonPropertyName("max_devices")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public int? MaxDevices { get; set; }

    [JsonPropertyName("expires_at")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public DateTime? ExpiresAt { get; set; }

    [JsonPropertyName("allowed_locations")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public IReadOnlyList<string>? AllowedLocations { get; set; }

    [JsonPropertyName("note")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? Note { get; set; }

    [JsonPropertyName("external_payment_id")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? ExternalPaymentId { get; set; }

    [JsonPropertyName("created_by")]
    public string CreatedBy { get; set; } = string.Empty;
}

public sealed class CreateLicenseResponse
{
    [JsonPropertyName("license_id")]
    public string LicenseId { get; set; } = string.Empty;

    /// <summary>Raw license token shown once; never persisted.</summary>
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;

    [JsonPropertyName("expires_at")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public DateTime? ExpiresAt { get; set; }
}

public sealed class ExtendLicenseRequest
{
    [JsonPropertyName("license_id")]
    public Guid LicenseId { get; set; }

    [JsonPropertyName("expires_at")]
    public DateTime ExpiresAt { get; set; }
}

public sealed class CreateBootstrapTokenRequest
{
    [JsonPropertyName("expires_at")]
    public DateTime ExpiresAt { get; set; }

    [JsonPropertyName("max_uses")]
    public int MaxUses { get; set; } = 1;

    [JsonPropertyName("allowed_location")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? AllowedLocation { get; set; }

    [JsonPropertyName("note")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? Note { get; set; }

    [JsonPropertyName("created_by")]
    public string CreatedBy { get; set; } = string.Empty;
}

public sealed class CreateBootstrapTokenResponse
{
    [JsonPropertyName("bootstrap_id")]
    public Guid BootstrapId { get; set; }

    /// <summary>Raw bootstrap token shown once; never persisted.</summary>
    [JsonPropertyName("bootstrap_token")]
    public string BootstrapToken { get; set; } = string.Empty;

    [JsonPropertyName("expires_at")]
    public DateTime ExpiresAt { get; set; }

    [JsonPropertyName("max_uses")]
    public int MaxUses { get; set; }
}

public sealed class BootstrapTokenListItemDto
{
    [JsonPropertyName("bootstrap_id")]
    public Guid BootstrapId { get; set; }

    [JsonPropertyName("expires_at")]
    public DateTime ExpiresAt { get; set; }

    [JsonPropertyName("max_uses")]
    public int MaxUses { get; set; }

    [JsonPropertyName("used_count")]
    public int UsedCount { get; set; }

    [JsonPropertyName("allowed_location")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? AllowedLocation { get; set; }

    [JsonPropertyName("status")]
    public string Status { get; set; } = string.Empty;

    [JsonPropertyName("created_at")]
    public DateTime CreatedAt { get; set; }

    [JsonPropertyName("created_by")]
    public string CreatedBy { get; set; } = string.Empty;

    [JsonPropertyName("note")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? Note { get; set; }
}

public sealed class AuditWriteRequest
{
    public string Actor { get; set; } = string.Empty;
    public string Action { get; set; } = string.Empty;
    public string? EntityType { get; set; }
    public string? EntityId { get; set; }
    public string? Detail { get; set; }
    public string? IpAddress { get; set; }
}

public sealed class SigningMaterialDto
{
    public string KeyId { get; set; } = string.Empty;
    public byte[] PrivateKey { get; set; } = Array.Empty<byte>();
    public byte[] PublicKey { get; set; } = Array.Empty<byte>();
}

public sealed class VerificationKeyDto
{
    public string KeyId { get; set; } = string.Empty;
    public byte[] PublicKey { get; set; } = Array.Empty<byte>();
    public string Status { get; set; } = string.Empty;
}

public sealed class RotateSigningKeyResult
{
    public string NewKeyId { get; set; } = string.Empty;
    public string PreviousKeyId { get; set; } = string.Empty;
}

public sealed class IssueTicketCommand
{
    public string LicenseId { get; set; } = string.Empty;
    public string DeviceId { get; set; } = string.Empty;
    public string Role { get; set; } = string.Empty;
    public string Plan { get; set; } = string.Empty;
    public IReadOnlyList<string> Permissions { get; set; } = Array.Empty<string>();
    public IReadOnlyList<string>? Locations { get; set; }
    public IReadOnlyList<string>? NodeScope { get; set; }
    public byte[] DevicePublicKey { get; set; } = Array.Empty<byte>();
}

public sealed class DeviceListItemDto
{
    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;

    [JsonPropertyName("license_id")]
    public string LicenseId { get; set; } = string.Empty;

    [JsonPropertyName("status")]
    public string Status { get; set; } = string.Empty;

    [JsonPropertyName("platform")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? Platform { get; set; }

    [JsonPropertyName("device_name")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? DeviceName { get; set; }

    [JsonPropertyName("created_at")]
    public DateTime CreatedAt { get; set; }

    [JsonPropertyName("last_seen_at")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public DateTime? LastSeenAt { get; set; }
}
