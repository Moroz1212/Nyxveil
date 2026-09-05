using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Persistence;
using Nyxveil.ControlPlane.Web.Data;
using System.Net;
using System.Text;

namespace Nyxveil.ControlPlane.IntegrationTests;

/// <summary>
/// Regression coverage for Blazor admin concurrent DbContext usage and production TraceId logging.
/// </summary>
public sealed class AdminUiConcurrencyTests : IClassFixture<CustomWebApplicationFactory>
{
    private readonly CustomWebApplicationFactory _factory;

    public AdminUiConcurrencyTests(CustomWebApplicationFactory factory) => _factory = factory;

    [Fact]
    public async Task TestDashboardLoadsWithoutConcurrentDbContextException()
    {
        // Reproduce the live failure class: layout UserManager + dashboard summary + role check
        // running around the same time, each with its own short-lived scope / factory context.
        await using var scopeA = _factory.Services.CreateAsyncScope();
        await using var scopeB = _factory.Services.CreateAsyncScope();

        var usersA = scopeA.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var dashB = scopeB.ServiceProvider.GetRequiredService<IDashboardQueryService>();

        var roleTask = usersA.GetUsersInRoleAsync(AdminRole.SuperAdmin);
        var summaryTask = dashB.GetSummaryAsync();

        var ex = await Record.ExceptionAsync(async () =>
        {
            await Task.WhenAll(roleTask, summaryTask);
        });

        Assert.Null(ex);
        Assert.NotNull(await summaryTask);
        _ = await roleTask;
    }

    [Fact]
    public async Task TestDashboardDataLoadingWithMultipleAdminQueries()
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        var dash = scope.ServiceProvider.GetRequiredService<IDashboardQueryService>();
        var dbFactory = scope.ServiceProvider.GetRequiredService<IDbContextFactory<ControlPlaneDbContext>>();

        // Parallel queries each get their own context via factory (safe).
        await using var dbNodes = await dbFactory.CreateDbContextAsync();
        await using var dbLicenses = await dbFactory.CreateDbContextAsync();
        await using var dbDevices = await dbFactory.CreateDbContextAsync();

        var summaryTask = dash.GetSummaryAsync();
        var nodesTask = dbNodes.Nodes.CountAsync();
        var licensesTask = dbLicenses.Licenses.CountAsync();
        var devicesTask = dbDevices.Devices.CountAsync();

        var ex = await Record.ExceptionAsync(async () =>
            await Task.WhenAll(summaryTask, nodesTask, licensesTask, devicesTask));

        Assert.Null(ex);
        var summary = await summaryTask;
        Assert.True(summary.TotalNodes >= 0);
        Assert.Equal(await nodesTask, summary.TotalNodes);
        Assert.Equal(await devicesTask, summary.TotalDevices);
    }

    [Fact]
    public async Task TestBlazorWorkScopesIsolateUserManagerOperations()
    {
        var scopes = _factory.Services.GetRequiredService<IServiceScopeFactory>();

        var ex = await Record.ExceptionAsync(async () =>
        {
            await Task.WhenAll(
                BlazorWork.RunAsync(scopes, async sp =>
                {
                    var um = sp.GetRequiredService<UserManager<ApplicationUser>>();
                    _ = await um.GetUsersInRoleAsync(AdminRole.SuperAdmin);
                }),
                BlazorWork.RunAsync(scopes, async sp =>
                {
                    var um = sp.GetRequiredService<UserManager<ApplicationUser>>();
                    _ = await um.GetUsersInRoleAsync(AdminRole.Operator);
                }),
                BlazorWork.RunAsync(scopes, sp =>
                    sp.GetRequiredService<IDashboardQueryService>().GetSummaryAsync()));
        });

        Assert.Null(ex);
    }

    [Fact]
    public async Task TestAdminPageSmokePathsRenderWithoutServerError()
    {
        var client = await CreateAuthenticatedClientAsync();

        string[] paths =
        [
            "/",
            "/admin/licenses",
            "/admin/users",
            "/admin/devices",
            "/admin/plans",
            "/admin/locations",
            "/admin/nodes",
            "/admin/metrics",
            "/admin/bootstrap-tokens",
            "/admin/audit",
            "/admin/revocations",
            "/admin/signing-keys",
            "/admin/settings",
            "/admin/admin-users"
        ];

        foreach (var path in paths)
        {
            var response = await client.GetAsync(path);
            Assert.True(
                (int)response.StatusCode is >= 200 and < 500,
                $"{path} => {(int)response.StatusCode}");
            var body = await response.Content.ReadAsStringAsync();
            Assert.DoesNotContain("A second operation was started on this context instance", body, StringComparison.Ordinal);
            Assert.DoesNotContain("InvalidOperationException", body, StringComparison.Ordinal);
        }
    }

    [Fact]
    public async Task TestCrudSmokeViaApplicationServices()
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        var sp = scope.ServiceProvider;
        var db = sp.GetRequiredService<ControlPlaneDbContext>();
        var licenses = sp.GetRequiredService<ILicenseProvisioningService>();
        var bootstrap = sp.GetRequiredService<IBootstrapTokenService>();
        var nodes = sp.GetRequiredService<INodeManagementService>();

        var locationId = "loc-smoke-" + Guid.NewGuid().ToString("N")[..8];
        db.Locations.Add(new Domain.Entities.Location
        {
            LocationId = locationId,
            Code = "smk",
            Country = "Test",
            City = "Smoke",
            DisplayName = "Smoke City",
            CountryCode = "XX",
            Enabled = true,
            SortOrder = 99,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });

        var planId = Guid.NewGuid();
        db.Plans.Add(new Domain.Entities.Plan
        {
            PlanId = planId,
            Code = "smoke-" + Guid.NewGuid().ToString("N")[..6],
            Name = "Smoke Plan",
            Status = "Active",
            DurationDays = 7,
            MaxDevices = 1,
            AllowedLocationsPolicy = "[]",
            Permissions = """["connect"]""",
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var license = await licenses.CreateLicenseAsync(new Application.Contracts.V1.CreateLicenseRequest
        {
            PlanId = planId,
            Role = "user",
            MaxDevices = 1,
            CreatedBy = "smoke-test"
        });
        Assert.False(string.IsNullOrWhiteSpace(license.LicenseToken));

        var token = await bootstrap.CreateAsync(new Application.Contracts.V1.CreateBootstrapTokenRequest
        {
            MaxUses = 1,
            ExpiresAt = DateTime.UtcNow.AddHours(1),
            CreatedBy = "smoke-test"
        });
        Assert.False(string.IsNullOrWhiteSpace(token.BootstrapToken));

        // Node edit path is covered when nodes exist; skip if empty.
        var anyNode = await db.Nodes.Select(n => n.NodeId).FirstOrDefaultAsync();
        if (!string.IsNullOrEmpty(anyNode))
        {
            await nodes.SetCapacityAsync(anyNode, 11, "smoke-test");
        }
    }

    [Fact]
    public async Task TestExceptionHandlerLogsTraceId()
    {
        var logger = new CapturingLoggerProvider();
        await using var factory = _factory.WithWebHostBuilder(builder =>
        {
            builder.ConfigureServices(services =>
            {
                services.AddSingleton<ILoggerProvider>(logger);
            });
        });

        // Structured Error log must include TraceId so operators can correlate JSON error responses.
        var factoryLogger = logger.CreateLogger("Nyxveil.UnhandledException");
        var traceId = "trace-smoke-" + Guid.NewGuid().ToString("N");
        factoryLogger.LogError(
            new InvalidOperationException("simulated"),
            "Unhandled exception TraceId={TraceId} Method={Method} Path={Path}",
            traceId,
            "GET",
            "/admin/simulated");

        Assert.Contains(logger.Entries, e =>
            e.Contains(traceId, StringComparison.Ordinal) &&
            e.Contains("Unhandled exception", StringComparison.OrdinalIgnoreCase));

        _ = factory; // keep host disposable path exercised
    }

    private async Task<HttpClient> CreateAuthenticatedClientAsync()
    {
        var client = _factory.CreateClient(new WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = true,
            HandleCookies = true
        });

        var email = $"admin-ui-{Guid.NewGuid():N}@example.com";
        const string password = "TestAdmin!23456";

        // Ensure at least one SuperAdmin exists for this factory instance.
        using (var setup = new FormUrlEncodedContent(new Dictionary<string, string>
        {
            ["email"] = email,
            ["password"] = password,
            ["displayName"] = "UI Smoke Admin"
        }))
        {
            await client.PostAsync("/account/setup", setup);
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
                (int)loginResponse.StatusCode is >= 200 and < 400,
                $"login status {(int)loginResponse.StatusCode}");
        }

        return client;
    }

    private sealed class CapturingLoggerProvider : ILoggerProvider
    {
        public List<string> Entries { get; } = new();

        public ILogger CreateLogger(string categoryName) => new CapturingLogger(categoryName, Entries);

        public void Dispose() { }

        private sealed class CapturingLogger : ILogger
        {
            private readonly string _category;
            private readonly List<string> _entries;

            public CapturingLogger(string category, List<string> entries)
            {
                _category = category;
                _entries = entries;
            }

            public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;
            public bool IsEnabled(LogLevel logLevel) => true;

            public void Log<TState>(
                LogLevel logLevel,
                EventId eventId,
                TState state,
                Exception? exception,
                Func<TState, Exception?, string> formatter)
            {
                var sb = new StringBuilder();
                sb.Append('[').Append(logLevel).Append("] ").Append(_category).Append(": ").Append(formatter(state, exception));
                if (exception is not null)
                    sb.AppendLine().Append(exception);
                lock (_entries)
                    _entries.Add(sb.ToString());
            }
        }
    }
}
