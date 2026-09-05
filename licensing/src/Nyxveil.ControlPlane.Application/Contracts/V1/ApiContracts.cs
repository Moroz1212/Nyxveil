using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

public sealed class LicenseValidateRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;
}

public sealed class LicenseValidateResponse
{
    [JsonPropertyName("valid")]
    public bool Valid { get; set; }

    [JsonPropertyName("license_id")]
    public string? LicenseId { get; set; }

    [JsonPropertyName("plan")]
    public string? Plan { get; set; }

    /// <summary>Current license role (user / master / test). Master access uses Role, not Plan.</summary>
    [JsonPropertyName("role")]
    public string? Role { get; set; }

    [JsonPropertyName("max_devices")]
    public int MaxDevices { get; set; }

    [JsonPropertyName("message")]
    public string? Message { get; set; }
}

public sealed class DeviceActivateRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;

    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;

    [JsonPropertyName("public_key")]
    public byte[] PublicKey { get; set; } = Array.Empty<byte>();

    [JsonPropertyName("platform")]
    public string? Platform { get; set; }

    [JsonPropertyName("device_name")]
    public string? DeviceName { get; set; }
}

public sealed class DeviceActivateResponse
{
    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;

    [JsonPropertyName("activated")]
    public bool Activated { get; set; }
}

public sealed class TicketIssueRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;

    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;

    [JsonPropertyName("node_id")]
    public string? NodeId { get; set; }

    [JsonPropertyName("location_id")]
    public string? LocationId { get; set; }
}

public sealed class TicketIssueResponse
{
    [JsonPropertyName("access_ticket")]
    public string AccessTicket { get; set; } = string.Empty;

    [JsonPropertyName("expires_at")]
    public long ExpiresAt { get; set; }

    [JsonPropertyName("node_id")]
    public string? NodeId { get; set; }
}

public sealed class TicketRefreshRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;

    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;

    [JsonPropertyName("access_ticket")]
    public string AccessTicket { get; set; } = string.Empty;

    [JsonPropertyName("refresh_hint")]
    public string? RefreshHint { get; set; }
}

public sealed class NodeHeartbeatRequest
{
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;

    [JsonPropertyName("node_token")]
    public string NodeToken { get; set; } = string.Empty;

    [JsonPropertyName("version")]
    public string Version { get; set; } = string.Empty;

    [JsonPropertyName("protocol_version")]
    public ushort ProtocolVersion { get; set; }

    [JsonPropertyName("capacity")]
    public int Capacity { get; set; }

    [JsonPropertyName("current_sessions")]
    public int CurrentSessions { get; set; }

    [JsonPropertyName("load")]
    public double Load { get; set; }

    [JsonPropertyName("cpu_usage")]
    public double? CpuUsage { get; set; }

    [JsonPropertyName("memory_usage")]
    public double? MemoryUsage { get; set; }

    [JsonPropertyName("memory_bytes")]
    public long? MemoryBytes { get; set; }

    [JsonPropertyName("uptime")]
    public long? Uptime { get; set; }

    [JsonPropertyName("network_rx_rate")]
    public double? NetworkRxRate { get; set; }

    [JsonPropertyName("network_tx_rate")]
    public double? NetworkTxRate { get; set; }

    [JsonPropertyName("timestamp")]
    public DateTime? Timestamp { get; set; }

    [JsonPropertyName("healthy")]
    public bool? Healthy { get; set; }
}

public sealed class NodeHeartbeatResponse
{
    [JsonPropertyName("accepted")]
    public bool Accepted { get; set; }

    [JsonPropertyName("status")]
    public string Status { get; set; } = string.Empty;

    [JsonPropertyName("config_version")]
    public long ConfigVersion { get; set; }
}

public sealed class RevocationListResponse
{
    [JsonPropertyName("revoked_jtis")]
    public List<string> RevokedJtis { get; set; } = new();

    [JsonPropertyName("revoked_licenses")]
    public List<string> RevokedLicenses { get; set; } = new();

    [JsonPropertyName("revoked_devices")]
    public List<string> RevokedDevices { get; set; } = new();

    [JsonPropertyName("updated_at")]
    public long UpdatedAt { get; set; }
}

public sealed class VersionResponse
{
    [JsonPropertyName("control_plane_version")]
    public string ControlPlaneVersion { get; set; } = "1.0.0";

    [JsonPropertyName("min_protocol_version")]
    public ushort MinProtocolVersion { get; set; } = 1;

    [JsonPropertyName("max_protocol_version")]
    public ushort MaxProtocolVersion { get; set; } = 1;

    [JsonPropertyName("recommended_client_version")]
    public string RecommendedClient { get; set; } = "1.0.0";
}
