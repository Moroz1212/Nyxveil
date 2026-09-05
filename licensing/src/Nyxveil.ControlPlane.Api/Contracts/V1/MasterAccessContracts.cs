using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Api.Contracts.V1;

/// <summary>Matches core MasterAccessRequest.</summary>
public sealed class MasterAccessRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = string.Empty;

    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = string.Empty;
}

/// <summary>Matches core MasterAccessResponse.</summary>
public sealed class MasterAccessResponse
{
    [JsonPropertyName("role")]
    public string Role { get; set; } = "master";

    [JsonPropertyName("granted")]
    public bool Granted { get; set; }
}
