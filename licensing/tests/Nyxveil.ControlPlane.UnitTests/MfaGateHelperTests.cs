using System.Security.Claims;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Web.Security;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class MfaGateHelperTests
{
    [Theory]
    [InlineData("/account/mfa")]
    [InlineData("/account/mfa/setup")]
    [InlineData("/account/mfa/step-up")]
    [InlineData("/account/logout")]
    [InlineData("/account/login")]
    [InlineData("/account/login-2fa")]
    [InlineData("/setup")]
    [InlineData("/_framework/blazor.web.js")]
    [InlineData("/_blazor")]
    [InlineData("/health/live")]
    [InlineData("/api/v1/catalog")]
    [InlineData("/hubs/node-status")]
    [InlineData("/app.css")]
    public void MfaPathRules_IsExempt_AllowsGateBypassPaths(string path)
    {
        Assert.True(MfaPathRules.IsExempt(path));
    }

    [Theory]
    [InlineData("/")]
    [InlineData("/admin/nodes")]
    [InlineData("/admin/operations")]
    [InlineData("/admin/signing-keys")]
    public void MfaPathRules_IsExempt_BlocksPanelPaths(string path)
    {
        Assert.False(MfaPathRules.IsExempt(path));
    }

    [Fact]
    public void StepUpGuard_RequiresStepUp_WhenCookieMissing()
    {
        var http = CreateHttp("u1");
        Assert.True(StepUpGuard.RequiresStepUp(http));
        Assert.True(StepUpGuard.RequiresStepUp(null));
    }

    [Fact]
    public void StepUpGuard_RequiresStepUp_FalseAfterGrant()
    {
        var http = CreateHttp("u1");
        StepUpGuard.Grant(http, TimeSpan.FromMinutes(5));

        var setCookie = http.Response.Headers.SetCookie.ToString();
        Assert.Contains(StepUpGuard.CookieName, setCookie, StringComparison.Ordinal);

        var value = ExtractCookieValue(setCookie, StepUpGuard.CookieName);
        http.Request.Headers.Cookie = $"{StepUpGuard.CookieName}={value}";
        Assert.False(StepUpGuard.RequiresStepUp(http));
    }

    [Fact]
    public void StepUpGuard_RequiresStepUp_WhenExpired()
    {
        var http = CreateHttp("u1");
        var protector = http.RequestServices.GetRequiredService<IDataProtectionProvider>()
            .CreateProtector(StepUpGuard.ProtectorPurpose);
        var expires = DateTimeOffset.UtcNow.AddMinutes(-1).ToUnixTimeSeconds();
        var payload = $"u1|{expires}";
        var raw = Convert.ToBase64String(protector.Protect(System.Text.Encoding.UTF8.GetBytes(payload)));
        http.Request.Headers.Cookie = $"{StepUpGuard.CookieName}={raw}";
        Assert.True(StepUpGuard.RequiresStepUp(http));
    }

    [Fact]
    public void AuthenticatorKeyFormatter_FormatsAndBuildsUri()
    {
        var formatted = AuthenticatorKeyFormatter.FormatKey("ABCD1234EFGH5678");
        Assert.Equal("abcd 1234 efgh 5678", formatted);

        var uri = AuthenticatorKeyFormatter.BuildOtpAuthUri("Nyxveil", "a@b.c", "SECRET");
        Assert.StartsWith("otpauth://totp/", uri, StringComparison.Ordinal);
        Assert.Contains("secret=SECRET", uri, StringComparison.Ordinal);
        Assert.Contains("issuer=Nyxveil", uri, StringComparison.Ordinal);
    }

    private static DefaultHttpContext CreateHttp(string userId)
    {
        var services = new ServiceCollection();
        services.AddDataProtection();
        var sp = services.BuildServiceProvider();
        var http = new DefaultHttpContext { RequestServices = sp };
        http.User = new ClaimsPrincipal(new ClaimsIdentity(
        [
            new Claim(ClaimTypes.NameIdentifier, userId),
            new Claim(ClaimTypes.Name, userId)
        ], "Test"));
        return http;
    }

    private static string ExtractCookieValue(string setCookieHeader, string name)
    {
        var marker = name + "=";
        var idx = setCookieHeader.IndexOf(marker, StringComparison.Ordinal);
        Assert.True(idx >= 0);
        var start = idx + marker.Length;
        var end = setCookieHeader.IndexOf(';', start);
        return end < 0 ? setCookieHeader[start..] : setCookieHeader[start..end];
    }
}
