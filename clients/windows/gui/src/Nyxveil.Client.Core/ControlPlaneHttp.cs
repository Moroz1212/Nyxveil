using System.Net.Http.Headers;

namespace Nyxveil.Client.Core;

/// <summary>
/// Creates HttpClient for Control Plane. Never sets InsecureSkipVerify / TrustAll —
/// uses the system trust store (and optional custom CA via handler if provided later).
/// </summary>
public static class ControlPlaneHttp
{
    public static HttpClient Create(Uri baseAddress, HttpMessageHandler? handler = null)
    {
        // Default HttpClientHandler validates certificates against SystemTrust.
        var http = handler is null ? new HttpClient() : new HttpClient(handler, disposeHandler: true);
        http.BaseAddress = baseAddress;
        http.Timeout = TimeSpan.FromSeconds(60);
        http.DefaultRequestHeaders.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
        http.DefaultRequestHeaders.UserAgent.ParseAdd("Nyxveil-Windows/1.1.2");
        return http;
    }

    public static ControlPlaneClient CreateClient(ClientSettings settings) =>
        new(Create(settings.GetBaseUri()));
}
