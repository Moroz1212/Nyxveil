using System.Net;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.IntegrationTests;

/// <summary>
/// Operator-panel HTTP smoke: authenticated page renders without server exceptions.
/// Not a headed browser; covers the same routes an operator opens after login.
/// </summary>
public sealed class OperatorPanelSmokeTests : IClassFixture<CustomWebApplicationFactory>
{
    private readonly CustomWebApplicationFactory _factory;

    public OperatorPanelSmokeTests(CustomWebApplicationFactory factory) => _factory = factory;

    [Fact]
    public async Task Operator_PanelPages_RenderWithoutServerError()
    {
        var client = await CreateRoleClientAsync(AdminRole.Operator);
        await using var scope = _factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var nodeId = await SeedActiveAndDeletedNodesAsync(db);

        string[] paths =
        [
            "/",
            "/admin/nodes",
            "/admin/locations",
            "/admin/metrics",
            "/admin/operations",
            "/admin/infrastructure",
            "/admin/fleet",
            "/admin/audit",
            "/admin/settings",
            $"/admin/nodes/{nodeId}",
            "/account/access-denied"
        ];

        foreach (var path in paths)
        {
            using var response = await client.GetAsync(path);
            Assert.True(
                (int)response.StatusCode is >= 200 and < 500,
                $"{path} => {(int)response.StatusCode}");
            var body = await response.Content.ReadAsStringAsync();
            Assert.DoesNotContain("A second operation was started on this context instance", body, StringComparison.Ordinal);
            Assert.DoesNotContain("InvalidOperationException", body, StringComparison.Ordinal);
            Assert.DoesNotContain("Unhandled exception", body, StringComparison.OrdinalIgnoreCase);
        }
    }

    [Fact]
    public async Task SuperAdminWithoutMfa_IsForcedToEnrollment()
    {
        var client = await CreateRoleClientAsync(AdminRole.SuperAdmin, enableMfa: false);
        using var response = await client.GetAsync("/admin/nodes", HttpCompletionOption.ResponseHeadersRead);
        // Middleware redirects to MFA setup; follow redirects and confirm enrollment surface.
        var body = await response.Content.ReadAsStringAsync();
        Assert.True((int)response.StatusCode is >= 200 and < 500);
        Assert.True(
            body.Contains("mfa", StringComparison.OrdinalIgnoreCase)
            || body.Contains("аутентификац", StringComparison.OrdinalIgnoreCase)
            || body.Contains("Authenticator", StringComparison.OrdinalIgnoreCase)
            || response.RequestMessage?.RequestUri?.AbsolutePath.Contains("mfa", StringComparison.OrdinalIgnoreCase) == true,
            "SuperAdmin without MFA must be gated to enrollment");
    }

    [Fact]
    public async Task MfaAndStepUpPages_RenderForSuperAdmin()
    {
        var client = await CreateRoleClientAsync(AdminRole.SuperAdmin, enableMfa: false);
        foreach (var path in new[] { "/account/mfa", "/account/mfa/setup", "/account/mfa/step-up" })
        {
            using var response = await client.GetAsync(path);
            Assert.True((int)response.StatusCode is >= 200 and < 500, $"{path} => {response.StatusCode}");
            var body = await response.Content.ReadAsStringAsync();
            Assert.DoesNotContain("InvalidOperationException", body, StringComparison.Ordinal);
        }
    }

    [Fact]
    public async Task DeletedNode_DirectUrl_DoesNotExposeServerDetails()
    {
        var client = await CreateRoleClientAsync(AdminRole.Operator);
        await using var scope = _factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var (_, deletedId) = await SeedActiveAndDeletedPairAsync(db);

        using var response = await client.GetAsync($"/admin/nodes/{deletedId}");
        Assert.True((int)response.StatusCode is >= 200 and < 500, $"deleted node => {response.StatusCode}");
        var body = await response.Content.ReadAsStringAsync();
        Assert.DoesNotContain("CurrentSessions", body, StringComparison.Ordinal);
        Assert.DoesNotContain($"REBOOT {deletedId}", body, StringComparison.Ordinal);
    }

    [Fact]
    public async Task ReadOnly_CannotSeeSigningKeyRotateControl()
    {
        var client = await CreateRoleClientAsync(AdminRole.ReadOnly);
        using var response = await client.GetAsync("/admin/signing-keys");
        Assert.True((int)response.StatusCode is >= 200 and < 500);
        var body = await response.Content.ReadAsStringAsync();
        Assert.DoesNotContain("Безопасно сменить ключ", body, StringComparison.Ordinal);
    }

    private async Task<HttpClient> CreateRoleClientAsync(string role, bool enableMfa = false)
    {
        var client = _factory.CreateClient(new WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = true,
            HandleCookies = true
        });

        using (var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = $"bootstrap-{Guid.NewGuid():N}@example.com",
            ["password"] = "TestAdmin!23456",
            ["displayName"] = "Bootstrap"
        }))
        {
            await client.PostAsync("/account/setup", setup);
        }

        var email = $"{role.ToLowerInvariant()}-{Guid.NewGuid():N}@example.com";
        const string password = "TestAdmin!23456";

        await using (var scope = _factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole>>();
            foreach (var r in new[] { AdminRole.SuperAdmin, AdminRole.Operator, AdminRole.ReadOnly })
            {
                if (!await roles.RoleExistsAsync(r))
                    await roles.CreateAsync(new IdentityRole(r));
            }

            var user = new ApplicationUser
            {
                UserName = email,
                Email = email,
                EmailConfirmed = true,
                DisplayName = role
            };
            var created = await users.CreateAsync(user, password);
            Assert.True(created.Succeeded, string.Join(';', created.Errors.Select(e => e.Description)));
            await users.AddToRoleAsync(user, role);
            if (enableMfa)
            {
                await users.ResetAuthenticatorKeyAsync(user);
                await users.SetTwoFactorEnabledAsync(user, true);
            }
        }

        using (var login = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["returnUrl"] = "/"
        }))
        {
            var loginResponse = await client.PostAsync("/account/login", login);
            Assert.True((int)loginResponse.StatusCode is >= 200 and < 500);
        }

        return client;
    }

    private static async Task<string> SeedActiveAndDeletedNodesAsync(ControlPlaneDbContext db)
    {
        var (active, _) = await SeedActiveAndDeletedPairAsync(db);
        return active;
    }

    private static async Task<(string ActiveId, string DeletedId)> SeedActiveAndDeletedPairAsync(ControlPlaneDbContext db)
    {
        var locId = "smoke-loc-" + Guid.NewGuid().ToString("N")[..8];
        var locCode = "c" + Guid.NewGuid().ToString("N")[..6];
        if (!await db.Locations.AnyAsync(l => l.LocationId == locId))
        {
            db.Locations.Add(new Location
            {
                LocationId = locId,
                Code = locCode,
                Country = "Test",
                City = "Smoke",
                DisplayName = "Smoke",
                Enabled = true,
                SortOrder = 1,
                CreatedAt = DateTime.UtcNow,
                UpdatedAt = DateTime.UtcNow
            });
        }

        var activeId = "node-active-" + Guid.NewGuid().ToString("N")[..8];
        var deletedId = "node-deleted-" + Guid.NewGuid().ToString("N")[..8];
        var now = DateTime.UtcNow;
        db.Nodes.Add(new Node
        {
            NodeId = activeId,
            DisplayName = "Active Smoke",
            LocationId = locId,
            LifecycleState = NodeLifecycleState.Active,
            Enabled = true,
            Capacity = 10,
            CreatedAt = now,
            UpdatedAt = now
        });
        db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = activeId,
            Enabled = true,
            Capacity = 10,
            ConfigVersion = 1,
            UpdatedAt = now
        });
        db.Nodes.Add(new Node
        {
            NodeId = deletedId,
            DisplayName = "Deleted Smoke",
            LocationId = locId,
            LifecycleState = NodeLifecycleState.Deleted,
            Enabled = false,
            Capacity = 10,
            DeletedAt = now,
            DeletedBy = "smoke",
            CreatedAt = now,
            UpdatedAt = now
        });
        db.NodeConfigs.Add(new NodeConfig
        {
            NodeId = deletedId,
            Enabled = false,
            Capacity = 10,
            ConfigVersion = 1,
            UpdatedAt = now
        });
        await db.SaveChangesAsync();
        return (activeId, deletedId);
    }
}
