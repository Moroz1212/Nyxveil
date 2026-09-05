using System.Net;
using System.Net.Http.Headers;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.IntegrationTests;

public sealed class ApiIntegrationTests : IClassFixture<CustomWebApplicationFactory>
{
    private readonly CustomWebApplicationFactory _factory;

    public ApiIntegrationTests(CustomWebApplicationFactory factory) => _factory = factory;

    [Fact]
    public async Task TestAnonymousCatalogRejected()
    {
        var client = _factory.CreateClient();
        var response = await client.GetAsync("/api/v1/catalog");
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task TestAnonymousRevocationRejected()
    {
        var client = _factory.CreateClient();
        var response = await client.GetAsync("/api/v1/revocation");
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task TestUserRevocationRejected()
    {
        var client = _factory.CreateClient();
        var token = await CreateLicenseTokenAsync();

        using var request = new HttpRequestMessage(HttpMethod.Get, "/api/v1/revocation");
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        request.Headers.Add("X-Node-Id", "some-node");

        var response = await client.SendAsync(request);
        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task TestAdminAuthentication()
    {
        var client = _factory.CreateClient(new WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
            HandleCookies = true
        });

        var email = $"admin-{Guid.NewGuid():N}@example.com";
        const string password = "TestAdmin!23456";

        using (var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["displayName"] = "Test Admin"
        }))
        {
            var setupResponse = await client.PostAsync("/account/setup", setup);
            // First SuperAdmin succeeds with redirect; subsequent factory reuse redirects to login.
            Assert.True(
                (int)setupResponse.StatusCode is >= 200 and < 400,
                $"setup status: {(int)setupResponse.StatusCode}");
        }

        using (var login = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["returnUrl"] = "/"
        }))
        {
            var loginResponse = await client.PostAsync("/account/login", login);
            Assert.True(
                loginResponse.StatusCode is HttpStatusCode.Redirect or HttpStatusCode.Found or HttpStatusCode.SeeOther,
                $"login status: {(int)loginResponse.StatusCode}");
        }
    }

    [Fact]
    public async Task TestHealthLive()
    {
        var client = _factory.CreateClient();
        var response = await client.GetAsync("/health/live");
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public async Task TestCreateLicense()
    {
        var token = await CreateLicenseTokenAsync();
        Assert.Contains(':', token);
        Assert.StartsWith("nyx_lic_", token);

        using var scope = _factory.Services.CreateScope();
        var licenses = scope.ServiceProvider.GetRequiredService<ILicenseProvisioningService>();
        var validated = await licenses.ValidateLicenseTokenAsync(new LicenseValidateRequest
        {
            LicenseToken = token
        });
        Assert.True(validated.Valid);
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
            CreatedBy = "integration-test"
        });

        return created.LicenseToken;
    }
}
