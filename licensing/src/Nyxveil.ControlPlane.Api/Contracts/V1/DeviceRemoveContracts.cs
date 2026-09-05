using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Api.Contracts.V1;

/// <summary>Matches core device/remove request body.</summary>
public sealed class DeviceRemoveRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;

    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;
}

public sealed class DeviceRemoveResponse
{
    [JsonPropertyName("removed")]
    public bool Removed { get; set; }
}
