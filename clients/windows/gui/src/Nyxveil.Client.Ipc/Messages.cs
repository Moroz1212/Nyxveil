using System.Text.Json.Serialization;

namespace Nyxveil.Client.Ipc;

/// <summary>Matches engine/internal/ipc/protocol.go.</summary>
public static class IpcProtocol
{
    public const int Version = 1;
    public const string PipeName = @"\\.\pipe\NyxveilClient";

    public const string TypeHello = "hello";
    public const string TypeStatus = "status";
    public const string TypeConnect = "connect";
    public const string TypeDisconnect = "disconnect";
    public const string TypeNeedAccessTicket = "need_access_ticket";
    public const string TypeProvideAccessTicket = "provide_access_ticket";
    public const string TypeAccessTicket = "access_ticket"; // legacy
    public const string TypeError = "error";
    public const string TypeCancel = "cancel";
}

public class IpcEnvelope
{
    [JsonPropertyName("v")]
    public int Version { get; set; } = IpcProtocol.Version;

    [JsonPropertyName("type")]
    public string Type { get; set; } = "";

    [JsonPropertyName("id")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? Id { get; set; }

    [JsonPropertyName("error")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? Error { get; set; }
}

public sealed class ConnectCommand : IpcEnvelope
{
    public ConnectCommand() => Type = IpcProtocol.TypeConnect;

    [JsonPropertyName("desired_location_id")]
    public string DesiredLocationId { get; set; } = "";

    [JsonPropertyName("access_ticket")]
    public string AccessTicket { get; set; } = "";

    [JsonPropertyName("signed_catalog_json")]
    public byte[] SignedCatalogJson { get; set; } = Array.Empty<byte>();

    [JsonPropertyName("catalog_keys")]
    public Dictionary<string, string> CatalogKeys { get; set; } = new();

    [JsonPropertyName("device_private_key")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public byte[]? DevicePrivateKey { get; set; }

    [JsonPropertyName("control_plane_host")]
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? ControlPlaneHost { get; set; }
}

public sealed class ProvideAccessTicketCommand : IpcEnvelope
{
    public ProvideAccessTicketCommand() => Type = IpcProtocol.TypeProvideAccessTicket;

    [JsonPropertyName("request_id")]
    public string RequestId { get; set; } = "";

    [JsonPropertyName("access_ticket")]
    public string AccessTicket { get; set; } = "";
}

public sealed class NeedAccessTicketMessage : IpcEnvelope
{
    [JsonPropertyName("request_id")]
    public string RequestId { get; set; } = "";

    [JsonPropertyName("location_id")]
    public string DesiredLocationId { get; set; } = "";

    [JsonPropertyName("reason")]
    public string Reason { get; set; } = "";

    [JsonPropertyName("state")]
    public string State { get; set; } = "";
}

public sealed class StatusSnapshotMessage : IpcEnvelope
{
    [JsonPropertyName("state")]
    public string State { get; set; } = "";

    [JsonPropertyName("location_id")]
    public string? LocationId { get; set; }

    [JsonPropertyName("node_id")]
    public string? NodeId { get; set; }

    [JsonPropertyName("transport")]
    public string? Transport { get; set; }

    [JsonPropertyName("last_error")]
    public string? LastError { get; set; }

    [JsonPropertyName("client_version")]
    public string? ClientVersion { get; set; }

    [JsonPropertyName("core_version")]
    public string? CoreVersion { get; set; }

    [JsonPropertyName("protocol")]
    public string? Protocol { get; set; }
}
