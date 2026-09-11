using System.Security.Claims;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Web.Security;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class CriticalOperationSecurityTests
{
    [Fact]
    public void UpdateNode_WithoutStepUp_Denied()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "u1", elevated: false, roles: [AdminRole.SuperAdmin]);
        var ex = Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.UpdateNode, [AdminRole.SuperAdmin]));
        Assert.Contains("step-up", ex.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void UpdateNode_WithValidStepUp_Allowed()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "u1", elevated: true, roles: [AdminRole.SuperAdmin]);
        authorizer.AssertAllowed(CriticalOperation.UpdateNode, [AdminRole.SuperAdmin]);
    }

    [Fact]
    public void UpdateNode_ExpiredStepUp_Denied()
    {
        var (authorizer, http) = CreateAuthorizer(userId: "u1", elevated: true, roles: [AdminRole.SuperAdmin]);
        // Overwrite with expired cookie for same user.
        GrantExpired(http, "u1");
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.UpdateNode, [AdminRole.SuperAdmin]));
    }

    [Fact]
    public void RollingUpdate_WithoutStepUp_Denied()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "u1", elevated: false, roles: [AdminRole.SuperAdmin]);
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.RollingUpdateStart, [AdminRole.SuperAdmin]));
    }

    [Fact]
    public void RollingUpdate_WithValidStepUp_Allowed()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "u1", elevated: true, roles: [AdminRole.SuperAdmin]);
        authorizer.AssertAllowed(CriticalOperation.RollingUpdateStart, [AdminRole.SuperAdmin]);
    }

    [Fact]
    public void SigningKeyMutate_WithoutStepUp_Denied()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "u1", elevated: false, roles: [AdminRole.SuperAdmin]);
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.SigningKeyMutate, [AdminRole.SuperAdmin]));
    }

    [Fact]
    public void SigningKeyList_DoesNotRequireStepUpPolicy()
    {
        // Read-only list is not a CriticalOperation — policy only covers mutations.
        Assert.False(CriticalOperationPolicy.ForNodeCommand(NodeCommandType.RenewCertificate).HasValue);
    }

    [Fact]
    public void SensitiveSetting_WithoutStepUp_Denied()
    {
        Assert.True(CriticalOperationPolicy.IsSensitiveSettingKey("auth.bootstrap_token"));
        var (authorizer, _) = CreateAuthorizer(userId: "u1", elevated: false, roles: [AdminRole.SuperAdmin]);
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.SensitiveSettingsMutate, [AdminRole.SuperAdmin]));
    }

    [Fact]
    public void Operator_CannotExecuteSuperAdminSecurityMutation()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "op1", elevated: true, roles: [AdminRole.Operator]);
        var ex = Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.SigningKeyMutate, [AdminRole.Operator]));
        Assert.Contains("insufficient role", ex.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void ReadOnly_CannotMutateCriticalOps()
    {
        var (authorizer, _) = CreateAuthorizer(userId: "ro1", elevated: true, roles: [AdminRole.ReadOnly]);
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.RestartService, [AdminRole.ReadOnly]));
    }

    [Fact]
    public void LogoutInvalidatesStepUp_ViaClear()
    {
        var (authorizer, http) = CreateAuthorizer(userId: "u1", elevated: true, roles: [AdminRole.SuperAdmin]);
        authorizer.AssertAllowed(CriticalOperation.DeleteNode, [AdminRole.SuperAdmin]);
        StepUpGuard.Clear(http);
        http.Request.Headers.Cookie = "";
        Assert.True(StepUpGuard.RequiresStepUp(http));
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.DeleteNode, [AdminRole.SuperAdmin]));
    }

    [Fact]
    public void StepUp_ExpiresByTtl()
    {
        var services = new ServiceCollection();
        services.AddDataProtection();
        var sp = services.BuildServiceProvider();
        var http = new DefaultHttpContext { RequestServices = sp };
        http.User = Principal("u1", AdminRole.SuperAdmin);
        StepUpGuard.Grant(http, TimeSpan.FromMilliseconds(1));
        var setCookie = http.Response.Headers.SetCookie.ToString();
        var value = ExtractCookieValue(setCookie, StepUpGuard.CookieName);
        Thread.Sleep(20);
        http.Request.Headers.Cookie = $"{StepUpGuard.CookieName}={value}";
        Assert.True(StepUpGuard.RequiresStepUp(http));
    }

    [Fact]
    public void StepUp_UserA_CannotAuthorize_UserB()
    {
        var services = new ServiceCollection();
        services.AddDataProtection();
        var sp = services.BuildServiceProvider();
        var httpA = new DefaultHttpContext { RequestServices = sp };
        httpA.User = Principal("user-a", AdminRole.SuperAdmin);
        StepUpGuard.Grant(httpA, TimeSpan.FromMinutes(5));
        var cookie = ExtractCookieValue(httpA.Response.Headers.SetCookie.ToString(), StepUpGuard.CookieName);

        var httpB = new DefaultHttpContext { RequestServices = sp };
        httpB.User = Principal("user-b", AdminRole.SuperAdmin);
        httpB.Request.Headers.Cookie = $"{StepUpGuard.CookieName}={cookie}";
        Assert.True(StepUpGuard.RequiresStepUp(httpB));

        var authorizer = new HttpCriticalOperationAuthorizer(new HttpContextAccessor { HttpContext = httpB });
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.UpdateNode, [AdminRole.SuperAdmin]));
    }

    [Fact]
    public void RolloutContinuation_BypassesUpdateNodeStepUp()
    {
        var authorizer = new DenyUnlessRolloutCriticalOperationAuthorizer();
        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.UpdateNode, [AdminRole.SuperAdmin]));

        using (RolloutContinuationScope.Begin())
        {
            authorizer.AssertAllowed(CriticalOperation.UpdateNode, [AdminRole.SuperAdmin]);
        }

        Assert.Throws<ForbiddenException>(() =>
            authorizer.AssertAllowed(CriticalOperation.RollingUpdateStart, [AdminRole.SuperAdmin]));
    }

    [Theory]
    [InlineData("ui.theme", false)]
    [InlineData("dashboard.refresh_seconds", false)]
    [InlineData("auth.mfa_policy", true)]
    [InlineData("bootstrap.token", true)]
    [InlineData("location_rollout:eu-1", true)]
    [InlineData("signing.next_prepublish", true)]
    public void SensitiveSettingClassification(string key, bool sensitive)
    {
        Assert.Equal(sensitive, CriticalOperationPolicy.IsSensitiveSettingKey(key));
    }

    [Fact]
    public void ForNodeCommand_MapsCriticalTypes()
    {
        Assert.Equal(CriticalOperation.UpdateNode, CriticalOperationPolicy.ForNodeCommand(NodeCommandType.UpdateNodeLatest));
        Assert.Equal(CriticalOperation.RebootHost, CriticalOperationPolicy.ForNodeCommand(NodeCommandType.RebootHost));
        Assert.Equal(CriticalOperation.RestartService, CriticalOperationPolicy.ForNodeCommand(NodeCommandType.RestartNyxveilService));
        Assert.Null(CriticalOperationPolicy.ForNodeCommand(NodeCommandType.RenewCertificate));
    }

    private static (HttpCriticalOperationAuthorizer Authorizer, DefaultHttpContext Http) CreateAuthorizer(
        string userId,
        bool elevated,
        string[] roles)
    {
        var services = new ServiceCollection();
        services.AddDataProtection();
        var sp = services.BuildServiceProvider();
        var http = new DefaultHttpContext { RequestServices = sp };
        http.User = Principal(userId, roles);
        if (elevated)
        {
            StepUpGuard.Grant(http, TimeSpan.FromMinutes(5));
            var value = ExtractCookieValue(http.Response.Headers.SetCookie.ToString(), StepUpGuard.CookieName);
            http.Request.Headers.Cookie = $"{StepUpGuard.CookieName}={value}";
        }

        var accessor = new HttpContextAccessor { HttpContext = http };
        return (new HttpCriticalOperationAuthorizer(accessor), http);
    }

    private static void GrantExpired(DefaultHttpContext http, string userId)
    {
        var protector = http.RequestServices.GetRequiredService<IDataProtectionProvider>()
            .CreateProtector(StepUpGuard.ProtectorPurpose);
        var expires = DateTimeOffset.UtcNow.AddMinutes(-1).ToUnixTimeSeconds();
        var payload = $"{userId}|{expires}";
        var protectedBytes = protector.Protect(System.Text.Encoding.UTF8.GetBytes(payload));
        http.Request.Headers.Cookie = $"{StepUpGuard.CookieName}={Convert.ToBase64String(protectedBytes)}";
    }

    private static ClaimsPrincipal Principal(string userId, params string[] roles)
    {
        var claims = new List<Claim>
        {
            new(ClaimTypes.NameIdentifier, userId),
            new(ClaimTypes.Name, userId + "@test.local")
        };
        claims.AddRange(roles.Select(r => new Claim(ClaimTypes.Role, r)));
        return new ClaimsPrincipal(new ClaimsIdentity(claims, authenticationType: "Test"));
    }

    private static string ExtractCookieValue(string setCookieHeader, string name)
    {
        // Set-Cookie: name=value; path=/; ...
        var marker = name + "=";
        var idx = setCookieHeader.IndexOf(marker, StringComparison.Ordinal);
        Assert.True(idx >= 0, "cookie missing in Set-Cookie: " + setCookieHeader);
        var start = idx + marker.Length;
        var end = setCookieHeader.IndexOf(';', start);
        return end < 0 ? setCookieHeader[start..] : setCookieHeader[start..end];
    }
}
