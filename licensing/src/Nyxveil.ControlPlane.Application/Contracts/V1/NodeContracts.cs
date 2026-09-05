using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Application.Contracts.V1;

public sealed class NodeRegisterRequest
{
    [JsonPropertyName("bootstrap_token")]
    public string BootstrapToken { get; set; } = string.Empty;

    /// <summary>
    /// Proof-of-possession for existing-node idempotent retry (Frozen Core node token).
    /// </summary>
    [JsonPropertyName("node_token")]
    public string? NodeToken { get; set; }

    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;

    [JsonPropertyName("location_id")]
    public string LocationId { get; set; } = string.Empty;

    [JsonPropertyName("display_name")]
    public string DisplayName { get; set; } = string.Empty;

    [JsonPropertyName("public_identity")]
    public byte[] PublicIdentity { get; set; } = Array.Empty<byte>();

    [JsonPropertyName("public_key")]
    public byte[] PublicKey { get; set; } = Array.Empty<byte>();

    [JsonPropertyName("server_name")]
    public string? ServerName { get; set; }

    [JsonPropertyName("spki_pin")]
    public byte[]? SpkiPin { get; set; }

    [JsonPropertyName("protocol_version")]
    public ushort ProtocolVersion { get; set; } = 1;

    [JsonPropertyName("server_version")]
    public string? ServerVersion { get; set; }

    [JsonPropertyName("capacity")]
    public int Capacity { get; set; } = 100;

    [JsonPropertyName("test_only")]
    public bool TestOnly { get; set; }

    [JsonPropertyName("endpoints")]
    public List<NodeEndpointDto> Endpoints { get; set; } = new();
}

public sealed class NodeEndpointDto
{
    [JsonPropertyName("host")]
    public string Host { get; set; } = string.Empty;

    [JsonPropertyName("port")]
    public int Port { get; set; }

    [JsonPropertyName("address_family")]
    public string AddressFamily { get; set; } = "hostname";

    [JsonPropertyName("priority")]
    public int Priority { get; set; }

    [JsonPropertyName("enabled")]
    public bool Enabled { get; set; } = true;
}

public sealed class NodeRegisterResponse
{
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;

    [JsonPropertyName("registered")]
    public bool Registered { get; set; } = true;

    [JsonPropertyName("node_token")]
    public string NodeToken { get; set; } = string.Empty;

    [JsonPropertyName("config_version")]
    public long ConfigVersion { get; set; }

    [JsonPropertyName("config")]
    public NodeConfigResponse? Config { get; set; }
}

public sealed class NodeConfigResponse
{
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;

    [JsonPropertyName("location_id")]
    public string LocationId { get; set; } = string.Empty;

    [JsonPropertyName("enabled")]
    public bool Enabled { get; set; }

    [JsonPropertyName("draining")]
    public bool Draining { get; set; }

    [JsonPropertyName("maintenance_mode")]
    public bool MaintenanceMode { get; set; }

    [JsonPropertyName("transport_policy_json")]
    public string TransportPolicyJson { get; set; } = "{}";

    [JsonPropertyName("ech_policy_json")]
    public string? EchPolicyJson { get; set; }

    [JsonPropertyName("mtu")]
    public int? Mtu { get; set; }

    [JsonPropertyName("capacity")]
    public int Capacity { get; set; }

    [JsonPropertyName("minimum_server_version")]
    public string? MinimumServerVersion { get; set; }

    [JsonPropertyName("minimum_protocol_version")]
    public ushort? MinimumProtocolVersion { get; set; }

    [JsonPropertyName("config_version")]
    public long ConfigVersion { get; set; }

    [JsonPropertyName("updated_at")]
    public DateTime UpdatedAt { get; set; }
}

/// <summary>
/// Access-ticket verification public keys for nodes (never private keys).
/// Compatible with Go <c>ticketkeys.File</c> plus <c>updated_at</c>.
/// </summary>
public sealed class NodeTicketKeysResponse
{
    [JsonPropertyName("issuer")]
    public string Issuer { get; set; } = string.Empty;

    /// <summary>kid → standard Base64 of 32-byte Ed25519 public key.</summary>
    [JsonPropertyName("keys")]
    public Dictionary<string, string> Keys { get; set; } = new();

    [JsonPropertyName("updated_at")]
    public long UpdatedAt { get; set; }
}
