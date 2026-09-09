using System.Net;
using System.Net.Http.Headers;
using System.Reflection;
using System.Security.Cryptography;
using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.Controllers.V1;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.IntegrationTests;

/// <summary>
/// HTTP auth matrix for CatalogController — prevents class-level [LicenseAuth]
/// from re-locking public GET /api/v1/catalog-keys (Server 1.1.x contract).
/// </summary>
public sealed class CatalogKeysAuthMatrixTests : IClassFixture<CustomWebApplicationFactory>
{
    private readonly CustomWebApplicationFactory _factory;

    public CatalogKeysAuthMatrixTests(CustomWebApplicationFactory factory) => _factory = factory;

    [Fact]
    public void CatalogController_DoesNotApplyClassLevelLicenseAuth()
    {
        Assert.Null(typeof(CatalogController).GetCustomAttribute<LicenseAuthAttribute>());
        Assert.NotNull(typeof(CatalogController).GetMethod(nameof(CatalogController.GetCatalog))!
            .GetCustomAttribute<LicenseAuthAttribute>());
        Assert.NotNull(typeof(CatalogController).GetMethod(nameof(CatalogController.GetLocations))!
            .GetCustomAttribute<LicenseAuthAttribute>());
        Assert.NotNull(typeof(CatalogController).GetMethod(nameof(CatalogController.GetNodes))!
            .GetCustomAttribute<LicenseAuthAttribute>());
        Assert.Null(typeof(CatalogController).GetMethod(nameof(CatalogController.GetCatalogKeys))!
            .GetCustomAttribute<LicenseAuthAttribute>());
        Assert.NotNull(typeof(CatalogController).GetMethod(nameof(CatalogController.GetCatalogKeys))!
            .GetCustomAttribute<RateLimitAttribute>());
    }

    [Fact]
    public async Task CatalogKeys_Anonymous_Returns200_WithPublicKeysOnly()
    {
        var client = _factory.CreateClient();
        var response = await client.GetAsync("/api/v1/catalog-keys");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var body = await response.Content.ReadAsStringAsync();
        using var doc = JsonDocument.Parse(body);
        var root = doc.RootElement;
        Assert.True(root.TryGetProperty("issuer", out _));
        Assert.True(root.TryGetProperty("keys", out var keys));
        Assert.True(root.TryGetProperty("updated_at", out _));
        Assert.False(root.TryGetProperty("private_key", out _));
        Assert.False(root.TryGetProperty("protected_private_key", out _));
        Assert.False(root.TryGetProperty("seed", out _));
        Assert.False(root.TryGetProperty("status", out _));
        Assert.True(keys.EnumerateObject().Any(), "expected at least one verification public key");

        foreach (var kv in keys.EnumerateObject())
        {
            var pub = Convert.FromBase64String(kv.Value.GetString()!);
            Assert.Equal(32, pub.Length);
        }
    }

    [Theory]
    [InlineData("/api/v1/catalog")]
    [InlineData("/api/v1/locations")]
    [InlineData("/api/v1/nodes")]
    public async Task ProtectedCatalogEndpoints_Anonymous_Returns401(string path)
    {
        var client = _factory.CreateClient();
        var response = await client.GetAsync(path);
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Theory]
    [InlineData("/api/v1/catalog")]
    [InlineData("/api/v1/locations")]
    [InlineData("/api/v1/nodes")]
    public async Task ProtectedCatalogEndpoints_LicenseAuth_Returns200(string path)
    {
        var client = _factory.CreateClient();
        var token = await CreateLicenseTokenAsync();
        using var request = new HttpRequestMessage(HttpMethod.Get, path);
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        var response = await client.SendAsync(request);
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Theory]
    [InlineData("/api/v1/catalog")]
    [InlineData("/api/v1/locations")]
    [InlineData("/api/v1/nodes")]
    public async Task ProtectedCatalogEndpoints_AccessTicketAuth_Returns200(string path)
    {
        var client = _factory.CreateClient();
        var ticket = await IssueAccessTicketAsync();
        using var request = new HttpRequestMessage(HttpMethod.Get, path);
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", ticket);
        var response = await client.SendAsync(request);
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public async Task Catalog_LicenseAuth_SignatureReferencesPublicCatalogKeys()
    {
        var client = _factory.CreateClient();
        var token = await CreateLicenseTokenAsync();

        var keysResp = await client.GetAsync("/api/v1/catalog-keys");
        Assert.Equal(HttpStatusCode.OK, keysResp.StatusCode);
        var keysBody = await keysResp.Content.ReadAsStringAsync();
        var keys = JsonSerializer.Deserialize<CatalogKeysResponse>(keysBody);
        Assert.NotNull(keys);
        Assert.NotEmpty(keys!.Keys);

        using var catReq = new HttpRequestMessage(HttpMethod.Get, "/api/v1/catalog");
        catReq.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        var catResp = await client.SendAsync(catReq);
        Assert.Equal(HttpStatusCode.OK, catResp.StatusCode);
        using var doc = JsonDocument.Parse(await catResp.Content.ReadAsStringAsync());
        var kid = doc.RootElement.GetProperty("key_id").GetString();
        Assert.False(string.IsNullOrWhiteSpace(kid));
        Assert.True(keys.Keys.ContainsKey(kid!), $"catalog key_id {kid} missing from public catalog-keys");
    }

    private async Task<string> IssueAccessTicketAsync()
    {
        using var scope = _factory.Services.CreateScope();
        var devices = scope.ServiceProvider.GetRequiredService<IDeviceService>();
        var tickets = scope.ServiceProvider.GetRequiredService<ITicketService>();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();

        var token = await CreateLicenseTokenAsync();
        var deviceId = "dev-" + Guid.NewGuid().ToString("N")[..16];
        await devices.ActivateAsync(new DeviceActivateRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            PublicKey = RandomNumberGenerator.GetBytes(32),
            Platform = "integration",
            DeviceName = "auth-matrix"
        });

        var locationId = await db.Locations.Where(l => l.Enabled).Select(l => l.LocationId).FirstAsync();
        var issued = await tickets.IssueAsync(new TicketIssueRequest
        {
            LicenseToken = token,
            DeviceId = deviceId,
            LocationId = locationId
        });
        Assert.False(string.IsNullOrWhiteSpace(issued.AccessTicket));
        return issued.AccessTicket;
    }

    private async Task<string> CreateLicenseTokenAsync()
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var licenses = scope.ServiceProvider.GetRequiredService<ILicenseProvisioningService>();

        var plan = await db.Plans.FirstOrDefaultAsync();
        if (plan is null)
        {
            plan = new Plan
            {
                PlanId = Guid.NewGuid(),
                Code = "standard",
                Name = "Standard",
                Status = "Active",
                DurationDays = 30,
                MaxDevices = 3,
                AllowedLocationsPolicy = "[]",
                Permissions = """["connect"]""",
                CreatedAt = DateTime.UtcNow,
                UpdatedAt = DateTime.UtcNow
            };
            db.Plans.Add(plan);

            if (!await db.Locations.AnyAsync())
            {
                db.Locations.Add(new Location
                {
                    LocationId = "loc-ams",
                    Code = "ams",
                    Country = "Netherlands",
                    City = "Amsterdam",
                    DisplayName = "Amsterdam",
                    Enabled = true,
                    SortOrder = 1,
                    CreatedAt = DateTime.UtcNow,
                    UpdatedAt = DateTime.UtcNow
                });
            }

            await db.SaveChangesAsync();
        }

        var locationCodes = await db.Locations.Select(l => l.Code).Take(1).ToListAsync();
        var created = await licenses.CreateLicenseAsync(new CreateLicenseRequest
        {
            PlanId = plan.PlanId,
            Role = "user",
            MaxDevices = 2,
            AllowedLocations = locationCodes,
            CreatedBy = "catalog-keys-auth-matrix"
        });
        return created.LicenseToken;
    }
}
