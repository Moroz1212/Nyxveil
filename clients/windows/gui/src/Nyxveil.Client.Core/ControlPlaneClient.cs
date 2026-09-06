using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json.Serialization;

namespace Nyxveil.Client.Core;

/// <summary>Control Plane client API (exact JSON contracts from licensing).</summary>
public sealed class ControlPlaneClient : IDisposable
{
    private readonly HttpClient _http;
    private readonly bool _ownsHttp;

    public ControlPlaneClient(HttpClient http, bool ownsHttp = true)
    {
        _http = http;
        _ownsHttp = ownsHttp;
    }

    public void Dispose()
    {
        if (_ownsHttp)
            _http.Dispose();
    }

    public async Task<LicenseValidateResponse> ValidateAsync(string licenseToken, CancellationToken ct = default)
    {
        var resp = await _http.PostAsJsonAsync("/api/v1/license/validate",
            new LicenseValidateRequest { LicenseToken = licenseToken }, ct).ConfigureAwait(false);
        await EnsureSuccessAsync(resp, ct).ConfigureAwait(false);
        return (await resp.Content.ReadFromJsonAsync<LicenseValidateResponse>(ct).ConfigureAwait(false))!;
    }

    public async Task<DeviceActivateResponse> ActivateDeviceAsync(DeviceActivateRequest req, CancellationToken ct = default)
    {
        var resp = await _http.PostAsJsonAsync("/api/v1/device/activate", req, ct).ConfigureAwait(false);
        await EnsureSuccessAsync(resp, ct).ConfigureAwait(false);
        return (await resp.Content.ReadFromJsonAsync<DeviceActivateResponse>(ct).ConfigureAwait(false))!;
    }

    public async Task<CatalogKeysResponse> GetCatalogKeysAsync(string licenseToken, CancellationToken ct = default)
    {
        using var msg = new HttpRequestMessage(HttpMethod.Get, "/api/v1/catalog-keys");
        msg.Headers.Authorization = new AuthenticationHeaderValue("Bearer", licenseToken);
        var resp = await _http.SendAsync(msg, ct).ConfigureAwait(false);
        await EnsureSuccessAsync(resp, ct).ConfigureAwait(false);
        return (await resp.Content.ReadFromJsonAsync<CatalogKeysResponse>(ct).ConfigureAwait(false))!;
    }

    public async Task<byte[]> GetCatalogRawAsync(string licenseToken, CancellationToken ct = default)
    {
        using var msg = new HttpRequestMessage(HttpMethod.Get, "/api/v1/catalog");
        msg.Headers.Authorization = new AuthenticationHeaderValue("Bearer", licenseToken);
        var resp = await _http.SendAsync(msg, ct).ConfigureAwait(false);
        await EnsureSuccessAsync(resp, ct).ConfigureAwait(false);
        return await resp.Content.ReadAsByteArrayAsync(ct).ConfigureAwait(false);
    }

    public async Task<TicketIssueResponse> IssueTicketAsync(TicketIssueRequest req, CancellationToken ct = default)
    {
        var resp = await _http.PostAsJsonAsync("/api/v1/ticket/issue", req, ct).ConfigureAwait(false);
        await EnsureSuccessAsync(resp, ct).ConfigureAwait(false);
        return (await resp.Content.ReadFromJsonAsync<TicketIssueResponse>(ct).ConfigureAwait(false))!;
    }

    private static async Task EnsureSuccessAsync(HttpResponseMessage resp, CancellationToken ct)
    {
        if (resp.IsSuccessStatusCode)
            return;
        var body = await resp.Content.ReadAsStringAsync(ct).ConfigureAwait(false);
        throw new HttpRequestException(
            $"Control Plane {(int)resp.StatusCode} {resp.ReasonPhrase}: {Truncate(body, 240)}");
    }

    private static string Truncate(string s, int max) =>
        string.IsNullOrEmpty(s) ? "" : s.Length <= max ? s : s[..max] + "…";
}

public sealed class LicenseValidateRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = "";
}

public sealed class LicenseValidateResponse
{
    [JsonPropertyName("valid")]
    public bool Valid { get; set; }
    [JsonPropertyName("license_id")]
    public string? LicenseId { get; set; }
    [JsonPropertyName("plan")]
    public string? Plan { get; set; }
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
    public string LicenseToken { get; set; } = "";
    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = "";
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
    public string DeviceId { get; set; } = "";
    [JsonPropertyName("activated")]
    public bool Activated { get; set; }
}

public sealed class CatalogKeysResponse
{
    [JsonPropertyName("issuer")]
    public string Issuer { get; set; } = "";
    [JsonPropertyName("keys")]
    public Dictionary<string, string> Keys { get; set; } = new();
    [JsonPropertyName("updated_at")]
    public long UpdatedAt { get; set; }
}

public sealed class TicketIssueRequest
{
    [JsonPropertyName("license_token")]
    public string LicenseToken { get; set; } = "";
    [JsonPropertyName("device_id")]
    public string DeviceId { get; set; } = "";
    [JsonPropertyName("node_id")]
    public string? NodeId { get; set; }
    [JsonPropertyName("location_id")]
    public string? LocationId { get; set; }
}

public sealed class TicketIssueResponse
{
    [JsonPropertyName("access_ticket")]
    public string AccessTicket { get; set; } = "";
    [JsonPropertyName("expires_at")]
    public long ExpiresAt { get; set; }
    [JsonPropertyName("node_id")]
    public string? NodeId { get; set; }
}
