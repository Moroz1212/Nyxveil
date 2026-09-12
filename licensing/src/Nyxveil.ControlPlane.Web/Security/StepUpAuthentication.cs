using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;

namespace Nyxveil.ControlPlane.Web.Security;

/// <summary>
/// User-bound step-up cookie: protects against cross-user cookie reuse after account switch.
/// Format (DataProtection): userId|expiresUnixSeconds
/// </summary>
public static class StepUpGuard
{
    public const string CookieName = "nyxveil_stepup";
    public const string ProtectorPurpose = "Nyxveil.ControlPlane.StepUp.v1";
    public static readonly TimeSpan Validity = TimeSpan.FromMinutes(5);

    public static bool RequiresStepUp(HttpContext? http)
    {
        if (http?.User?.Identity?.IsAuthenticated != true)
            return true;

        var userId = http.User.FindFirstValue(ClaimTypes.NameIdentifier);
        if (string.IsNullOrEmpty(userId))
            return true;

        if (!http.Request.Cookies.TryGetValue(CookieName, out var raw) || string.IsNullOrWhiteSpace(raw))
            return true;

        try
        {
            var protector = http.RequestServices.GetRequiredService<IDataProtectionProvider>()
                .CreateProtector(ProtectorPurpose);
            var payload = Encoding.UTF8.GetString(protector.Unprotect(Convert.FromBase64String(raw)));
            var parts = payload.Split('|', 2);
            if (parts.Length != 2)
                return true;
            if (!string.Equals(parts[0], userId, StringComparison.Ordinal))
                return true;
            if (!long.TryParse(parts[1], out var expiresUnix))
                return true;
            return DateTimeOffset.UtcNow.ToUnixTimeSeconds() >= expiresUnix;
        }
        catch (CryptographicException)
        {
            return true;
        }
        catch (FormatException)
        {
            return true;
        }
    }

    public static void Grant(HttpContext http, TimeSpan? validity = null)
    {
        var userId = http.User.FindFirstValue(ClaimTypes.NameIdentifier)
                     ?? throw new InvalidOperationException("authenticated user required for step-up");
        var window = validity ?? Validity;
        var expires = DateTimeOffset.UtcNow.Add(window);
        var protector = http.RequestServices.GetRequiredService<IDataProtectionProvider>()
            .CreateProtector(ProtectorPurpose);
        var payload = $"{userId}|{expires.ToUnixTimeSeconds()}";
        var protectedBytes = protector.Protect(Encoding.UTF8.GetBytes(payload));
        http.Response.Cookies.Append(
            CookieName,
            Convert.ToBase64String(protectedBytes),
            new CookieOptions
            {
                HttpOnly = true,
                Secure = true,
                SameSite = SameSiteMode.Lax,
                Expires = expires,
                IsEssential = true,
                Path = "/"
            });
    }

    public static void Clear(HttpContext http)
    {
        http.Response.Cookies.Delete(CookieName, new CookieOptions
        {
            Secure = true,
            SameSite = SameSiteMode.Lax,
            Path = "/"
        });
    }
}

public interface IStepUpAuthenticationService
{
    bool IsElevated(HttpContext http);
    Task<bool> TryElevateWithAuthenticatorAsync(HttpContext http, string code, CancellationToken cancellationToken = default);
}

public sealed class StepUpAuthenticationService : IStepUpAuthenticationService
{
    private readonly UserManager<ApplicationUser> _userManager;

    public StepUpAuthenticationService(UserManager<ApplicationUser> userManager) =>
        _userManager = userManager;

    public bool IsElevated(HttpContext http) => !StepUpGuard.RequiresStepUp(http);

    public async Task<bool> TryElevateWithAuthenticatorAsync(
        HttpContext http,
        string code,
        CancellationToken cancellationToken = default)
    {
        _ = cancellationToken;
        var user = await _userManager.GetUserAsync(http.User).ConfigureAwait(false);
        if (user is null || !await _userManager.GetTwoFactorEnabledAsync(user).ConfigureAwait(false))
            return false;

        var normalized = (code ?? string.Empty).Replace(" ", string.Empty, StringComparison.Ordinal).Trim();
        if (string.IsNullOrEmpty(normalized))
            return false;

        var ok = await _userManager.VerifyTwoFactorTokenAsync(
            user,
            _userManager.Options.Tokens.AuthenticatorTokenProvider,
            normalized).ConfigureAwait(false);

        if (!ok)
            return false;

        StepUpGuard.Grant(http);
        return true;
    }
}

public sealed class HttpCriticalOperationAuthorizer : ICriticalOperationAuthorizer
{
    private readonly IHttpContextAccessor _http;

    public HttpCriticalOperationAuthorizer(IHttpContextAccessor http) => _http = http;

    public void AssertAllowed(CriticalOperation operation, IEnumerable<string>? roles = null)
    {
        if (operation == CriticalOperation.UpdateNode && RolloutContinuationScope.IsActive)
            return;

        var roleList = roles?
            .Where(r => !string.IsNullOrWhiteSpace(r))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToArray() ?? Array.Empty<string>();

        var http = _http.HttpContext;
        if (roleList.Length == 0 && http?.User is { Identity.IsAuthenticated: true } user)
        {
            roleList = user.FindAll(ClaimTypes.Role).Select(c => c.Value).ToArray();
            if (roleList.Length == 0)
            {
                // Fallback for role claims stored under short "role" type in some hosts.
                roleList = user.Claims
                    .Where(c => string.Equals(c.Type, "role", StringComparison.OrdinalIgnoreCase)
                                || c.Type.EndsWith("/role", StringComparison.OrdinalIgnoreCase))
                    .Select(c => c.Value)
                    .Distinct(StringComparer.OrdinalIgnoreCase)
                    .ToArray();
            }
        }

        if (CriticalOperationPolicy.RequiresSuperAdmin(operation)
            && !roleList.Contains(AdminRole.SuperAdmin, StringComparer.OrdinalIgnoreCase))
            throw new ForbiddenException("insufficient role for this operation");

        if (!CriticalOperationPolicy.RequiresSuperAdmin(operation)
            && !roleList.Contains(AdminRole.SuperAdmin, StringComparer.OrdinalIgnoreCase)
            && !roleList.Contains(AdminRole.Operator, StringComparer.OrdinalIgnoreCase))
            throw new ForbiddenException("insufficient role for this operation");

        if (http is null)
            throw new ForbiddenException("step-up authentication required");

        if (StepUpGuard.RequiresStepUp(http))
            throw new ForbiddenException("step-up authentication required");
    }
}

public static class AuthenticatorKeyFormatter
{
    public static string FormatKey(string unformattedKey)
    {
        if (string.IsNullOrEmpty(unformattedKey))
            return string.Empty;

        var result = new System.Text.StringBuilder();
        var currentPosition = 0;
        while (currentPosition + 4 < unformattedKey.Length)
        {
            result.Append(unformattedKey.AsSpan(currentPosition, 4)).Append(' ');
            currentPosition += 4;
        }

        if (currentPosition < unformattedKey.Length)
            result.Append(unformattedKey.AsSpan(currentPosition));

        return result.ToString().ToLowerInvariant();
    }

    public static string BuildOtpAuthUri(string issuer, string email, string unformattedKey)
    {
        var encIssuer = Uri.EscapeDataString(issuer);
        var encEmail = Uri.EscapeDataString(email);
        var encSecret = Uri.EscapeDataString(unformattedKey);
        return $"otpauth://totp/{encIssuer}:{encEmail}?secret={encSecret}&issuer={encIssuer}&digits=6";
    }
}

public static class CriticalOperationAuthRegistration
{
    public static IServiceCollection AddHttpCriticalOperationAuthorizer(this IServiceCollection services)
    {
        services.AddHttpContextAccessor();
        services.RemoveAll<ICriticalOperationAuthorizer>();
        services.AddSingleton<ICriticalOperationAuthorizer, HttpCriticalOperationAuthorizer>();
        return services;
    }
}
