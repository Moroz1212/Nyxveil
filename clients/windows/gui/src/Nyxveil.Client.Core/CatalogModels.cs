using System.Text.Json.Serialization;

namespace Nyxveil.Client.Core;

/// <summary>DTOs matching Control Plane / Frozen Core catalog JSON tags.</summary>
public sealed class LocationDto
{
    [JsonPropertyName("location_id")]
    public string LocationId { get; set; } = "";

    [JsonPropertyName("country")]
    public string Country { get; set; } = "";

    [JsonPropertyName("country_code")]
    public string CountryCode { get; set; } = "";

    [JsonPropertyName("city")]
    public string City { get; set; } = "";

    [JsonPropertyName("display_name")]
    public string DisplayName { get; set; } = "";

    [JsonPropertyName("enabled")]
    public bool Enabled { get; set; }
}

public sealed class EndpointDto
{
    [JsonPropertyName("host")]
    public string Host { get; set; } = "";

    [JsonPropertyName("port")]
    public int Port { get; set; }

    [JsonPropertyName("profiles")]
    public IReadOnlyList<string> Profiles { get; set; } = Array.Empty<string>();

    [JsonPropertyName("ip_family")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? IpFamily { get; set; }
}

public sealed class HealthInfoDto
{
    [JsonPropertyName("healthy")]
    public bool Healthy { get; set; }

    [JsonPropertyName("latency_ms")]
    public double LatencyMs { get; set; }

    [JsonPropertyName("session_count")]
    public int SessionCount { get; set; }

    [JsonPropertyName("cpu_percent")]
    public double CpuPercent { get; set; }

    [JsonPropertyName("memory_percent")]
    public double MemoryPercent { get; set; }
}

public sealed class NodeRegistryEntryDto
{
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = "";

    [JsonPropertyName("location_id")]
    public string LocationId { get; set; } = "";

    [JsonPropertyName("country")]
    public string Country { get; set; } = "";

    [JsonPropertyName("city")]
    public string City { get; set; } = "";

    [JsonPropertyName("display_name")]
    public string DisplayName { get; set; } = "";

    [JsonPropertyName("status")]
    public string Status { get; set; } = "";

    [JsonPropertyName("enabled")]
    public bool Enabled { get; set; }

    [JsonPropertyName("test_only")]
    public bool TestOnly { get; set; }

    [JsonPropertyName("draining")]
    public bool Draining { get; set; }

    [JsonPropertyName("protocol_version")]
    public ushort ProtocolVersion { get; set; }

    [JsonPropertyName("server_version")]
    public string ServerVersion { get; set; } = "";

    [JsonPropertyName("endpoints")]
    public IReadOnlyList<EndpointDto> Endpoints { get; set; } = Array.Empty<EndpointDto>();

    [JsonPropertyName("server_name")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? ServerName { get; set; }

    [JsonPropertyName("spki_pin")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public byte[]? SpkiPin { get; set; }

    [JsonPropertyName("capacity")]
    public int Capacity { get; set; }

    [JsonPropertyName("current_sessions")]
    public int CurrentSessions { get; set; }

    [JsonPropertyName("health")]
    public HealthInfoDto Health { get; set; } = new();

    [JsonPropertyName("last_seen")]
    public DateTime LastSeen { get; set; }
}

public sealed class CatalogDto
{
    [JsonPropertyName("version")]
    public string Version { get; set; } = "";

    [JsonPropertyName("locations")]
    public IReadOnlyList<LocationDto> Locations { get; set; } = Array.Empty<LocationDto>();

    [JsonPropertyName("nodes")]
    public IReadOnlyList<NodeRegistryEntryDto> Nodes { get; set; } = Array.Empty<NodeRegistryEntryDto>();

    [JsonPropertyName("issued_at")]
    public DateTime IssuedAt { get; set; }

    [JsonPropertyName("expires_at")]
    public DateTime ExpiresAt { get; set; }
}

public sealed class SignedCatalogDto
{
    [JsonPropertyName("catalog")]
    public CatalogDto Catalog { get; set; } = new();

    [JsonPropertyName("key_id")]
    public string KeyId { get; set; } = "";

    [JsonPropertyName("signature")]
    public byte[] Signature { get; set; } = Array.Empty<byte>();
}
