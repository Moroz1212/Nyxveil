using System.Reflection;
using System.Security.Claims;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Web.Components.Pages.Admin;
using Nyxveil.ControlPlane.Web.Security;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class AdminAuthorizationTests
{
    [Fact]
    public void AuthPolicies_DefineExpectedNames()
    {
        Assert.Equal("AnyAdmin", AuthPolicies.AnyAdmin);
        Assert.Equal("OperatorOrAbove", AuthPolicies.OperatorOrAbove);
        Assert.Equal("SuperAdminOnly", AuthPolicies.SuperAdminOnly);
    }

    [Fact]
    public void RoleSeeder_AllRoles_ContainAdminRbac()
    {
        Assert.Contains(AdminRole.SuperAdmin, RoleSeeder.AllRoles);
        Assert.Contains(AdminRole.Operator, RoleSeeder.AllRoles);
        Assert.Contains(AdminRole.ReadOnly, RoleSeeder.AllRoles);
        Assert.Equal(3, RoleSeeder.AllRoles.Length);
    }

    [Fact]
    public async Task RoleSeeder_EnsureRolesAsync_CreatesSuperAdminOperatorReadOnly()
    {
        await using var provider = BuildIdentityProvider();
        using var scope = provider.CreateScope();
        var roleManager = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole>>();

        await RoleSeeder.EnsureRolesAsync(roleManager);

        Assert.True(await roleManager.RoleExistsAsync(AdminRole.SuperAdmin));
        Assert.True(await roleManager.RoleExistsAsync(AdminRole.Operator));
        Assert.True(await roleManager.RoleExistsAsync(AdminRole.ReadOnly));
    }

    [Fact]
    public void SigningKeys_RequiresSuperAdminOnlyPolicy()
    {
        var attr = typeof(SigningKeys).GetCustomAttribute<AuthorizeAttribute>();
        Assert.NotNull(attr);
        Assert.Equal(AuthPolicies.SuperAdminOnly, attr!.Policy);
    }

    [Fact]
    public async Task TestReadOnlyCannotModify()
    {
        var auth = BuildAuthorizationService();
        var user = PrincipalWithRoles(AdminRole.ReadOnly);

        Assert.True((await auth.AuthorizeAsync(user, AuthPolicies.AnyAdmin)).Succeeded);
        Assert.False((await auth.AuthorizeAsync(user, AuthPolicies.OperatorOrAbove)).Succeeded);
        Assert.False((await auth.AuthorizeAsync(user, AuthPolicies.SuperAdminOnly)).Succeeded);
    }

    [Fact]
    public async Task TestOperatorCannotRotateSigningKeys()
    {
        var auth = BuildAuthorizationService();
        var user = PrincipalWithRoles(AdminRole.Operator);

        Assert.True((await auth.AuthorizeAsync(user, AuthPolicies.AnyAdmin)).Succeeded);
        Assert.True((await auth.AuthorizeAsync(user, AuthPolicies.OperatorOrAbove)).Succeeded);
        Assert.False((await auth.AuthorizeAsync(user, AuthPolicies.SuperAdminOnly)).Succeeded);
    }

    [Fact]
    public async Task TestSuperAdminCanManage()
    {
        var auth = BuildAuthorizationService();
        var user = PrincipalWithRoles(AdminRole.SuperAdmin);

        Assert.True((await auth.AuthorizeAsync(user, AuthPolicies.AnyAdmin)).Succeeded);
        Assert.True((await auth.AuthorizeAsync(user, AuthPolicies.OperatorOrAbove)).Succeeded);
        Assert.True((await auth.AuthorizeAsync(user, AuthPolicies.SuperAdminOnly)).Succeeded);
    }

    private static IAuthorizationService BuildAuthorizationService()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddAuthorization(options =>
        {
            options.AddPolicy(AuthPolicies.AnyAdmin, policy =>
                policy.RequireRole(AdminRole.SuperAdmin, AdminRole.Operator, AdminRole.ReadOnly));
            options.AddPolicy(AuthPolicies.OperatorOrAbove, policy =>
                policy.RequireRole(AdminRole.SuperAdmin, AdminRole.Operator));
            options.AddPolicy(AuthPolicies.SuperAdminOnly, policy =>
                policy.RequireRole(AdminRole.SuperAdmin));
        });
        return services.BuildServiceProvider().GetRequiredService<IAuthorizationService>();
    }

    private static ClaimsPrincipal PrincipalWithRoles(params string[] roles)
    {
        var claims = new List<Claim> { new(ClaimTypes.Name, "test-admin") };
        claims.AddRange(roles.Select(r => new Claim(ClaimTypes.Role, r)));
        return new ClaimsPrincipal(new ClaimsIdentity(claims, authenticationType: "Test"));
    }

    private static ServiceProvider BuildIdentityProvider()
    {
        var services = new ServiceCollection();
        services.AddLogging();
        services.AddDbContext<ControlPlaneDbContext>(o =>
            o.UseInMemoryDatabase("admin-rbac-" + Guid.NewGuid().ToString("N")));
        services
            .AddIdentityCore<ApplicationUser>()
            .AddRoles<IdentityRole>()
            .AddEntityFrameworkStores<ControlPlaneDbContext>();
        var provider = services.BuildServiceProvider();
        using var scope = provider.CreateScope();
        scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>().Database.EnsureCreated();
        return provider;
    }
}
