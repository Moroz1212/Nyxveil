using System.Net;
using System.Security.Cryptography;
using System.Text;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Hosting;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Hosting;

/// <summary>
/// Guards anonymous SuperAdmin creation via web (/setup, POST /account/setup).
/// </summary>
public static class WebBootstrapGuard
{
    public const string BootstrapTokenHeaderName = "X-Setup-Bootstrap-Token";
    public const string BootstrapTokenFormField = "bootstrapToken";

    public static SetupOptions ReadSetupOptions(IConfiguration configuration) =>
        configuration.GetSection(SetupOptions.SectionName).Get<SetupOptions>() ?? new SetupOptions();

    /// <summary>
    /// Production: localhost + AllowWebBootstrap + no SuperAdmin + valid high-entropy token.
    /// Development: AllowWebBootstrap + no SuperAdmin (local convenience; token optional).
    /// </summary>
    public static bool IsWebBootstrapAllowed(
        HttpContext http,
        IHostEnvironment environment,
        SetupOptions setup,
        bool superAdminExists,
        string? providedToken = null)
    {
        if (superAdminExists)
            return false;

        if (!setup.AllowWebBootstrap)
            return false;

        if (environment.IsDevelopment())
            return true;

        if (!IsLocalhost(http))
            return false;

        return TokenMatches(setup.BootstrapToken, providedToken ?? ExtractToken(http));
    }

    public static string? ExtractToken(HttpContext http)
    {
        if (http.Request.Headers.TryGetValue(BootstrapTokenHeaderName, out var header) &&
            !string.IsNullOrWhiteSpace(header))
        {
            return header.ToString();
        }

        if (http.Request.HasFormContentType)
        {
            // Form may already have been read by the caller; try Features if needed.
            try
            {
                if (http.Request.Form.TryGetValue(BootstrapTokenFormField, out var formVal) &&
                    !string.IsNullOrWhiteSpace(formVal))
                {
                    return formVal.ToString();
                }
            }
            catch (InvalidOperationException)
            {
                // Form not read yet — caller should pass providedToken after ReadFormAsync.
            }
        }

        return null;
    }

    public static bool IsLocalhost(HttpContext http)
    {
        var remote = http.Connection.RemoteIpAddress;
        if (remote is null)
            return false;

        if (IPAddress.IsLoopback(remote))
            return true;

        // IPv4-mapped IPv6 loopback
        if (remote.IsIPv4MappedToIPv6 && IPAddress.IsLoopback(remote.MapToIPv4()))
            return true;

        return false;
    }

    public static bool TokenMatches(string? configured, string? provided)
    {
        if (string.IsNullOrWhiteSpace(configured) || configured.Trim().Length < 32)
            return false;

        if (string.IsNullOrWhiteSpace(provided))
            return false;

        var a = Encoding.UTF8.GetBytes(configured.Trim());
        var b = Encoding.UTF8.GetBytes(provided.Trim());
        if (a.Length != b.Length)
            return false;

        return CryptographicOperations.FixedTimeEquals(a, b);
    }
}
