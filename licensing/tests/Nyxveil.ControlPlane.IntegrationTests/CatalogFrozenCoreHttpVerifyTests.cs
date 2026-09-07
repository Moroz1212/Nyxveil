using System.Diagnostics;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text.Json;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.IntegrationTests;

/// <summary>
/// Release-consumer gate: exact HTTP catalog bytes must pass Frozen Core catalog.Verify.
/// </summary>
public sealed class CatalogFrozenCoreHttpVerifyTests : IClassFixture<CustomWebApplicationFactory>
{
    private readonly CustomWebApplicationFactory _factory;

    public CatalogFrozenCoreHttpVerifyTests(CustomWebApplicationFactory factory) => _factory = factory;

    [Fact]
    public async Task HttpCatalog_PassesFrozenCoreGoVerifier()
    {
        var go = FindGo();
        Assert.False(string.IsNullOrWhiteSpace(go), "go toolchain required for Frozen Core catalog.Verify release gate");

        var token = await CreateLicenseTokenAsync();
        await RegisterCatalogNodeAsync();

        var client = _factory.CreateClient();
        using var catalogReq = new HttpRequestMessage(HttpMethod.Get, "/api/v1/catalog");
        catalogReq.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        var catalogResp = await client.SendAsync(catalogReq);
        catalogResp.EnsureSuccessStatusCode();
        var catalogBytes = await catalogResp.Content.ReadAsByteArrayAsync();

        using var keysReq = new HttpRequestMessage(HttpMethod.Get, "/api/v1/catalog-keys");
        keysReq.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        var keysResp = await client.SendAsync(keysReq);
        keysResp.EnsureSuccessStatusCode();
        var keysBytes = await keysResp.Content.ReadAsByteArrayAsync();

        using var keysDoc = JsonDocument.Parse(keysBytes);
        using var catalogDoc = JsonDocument.Parse(catalogBytes);
        var kid = catalogDoc.RootElement.GetProperty("key_id").GetString()!;
        var pubB64 = keysDoc.RootElement.GetProperty("keys").GetProperty(kid).GetString()!;

        var work = Path.Combine(Path.GetTempPath(), "nyxveil-http-catalog-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(work);
        var catalogPath = Path.Combine(work, "catalog.json");
        await File.WriteAllBytesAsync(catalogPath, catalogBytes);

        var verifyDir = LocateVerifySignedDir();
        Assert.True(Directory.Exists(verifyDir), "missing verify-signed at " + verifyDir);

        var repoRoot = Path.GetFullPath(Path.Combine(verifyDir, "..", "..", "..", ".."));
        Assert.True(File.Exists(Path.Combine(repoRoot, "go.mod")), "repo root go.mod missing: " + repoRoot);
        await File.WriteAllTextAsync(Path.Combine(verifyDir, "go.mod"),
            "module github.com/nyxveil/controlplane-verify-signed\n\n" +
            "go 1.24\n\n" +
            "require github.com/nyxveil/nvp v0.0.0\n\n" +
            "replace github.com/nyxveil/nvp => " + repoRoot.Replace('\\', '/') + "\n");

        var psi = new ProcessStartInfo
        {
            FileName = go!,
            Arguments = $"run . --catalog \"{catalogPath}\" --kid \"{kid}\" --pubkey-b64 \"{pubB64}\"",
            WorkingDirectory = verifyDir,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false
        };
        using var proc = Process.Start(psi)!;
        var stdout = await proc.StandardOutput.ReadToEndAsync();
        var stderr = await proc.StandardError.ReadToEndAsync();
        await proc.WaitForExitAsync();
        Assert.True(proc.ExitCode == 0,
            $"Frozen Core verify failed exit={proc.ExitCode}\nSTDOUT:\n{stdout}\nSTDERR:\n{stderr}");
        Assert.Contains("Catalog signature ........ PASS", stdout);

        var node = catalogDoc.RootElement.GetProperty("catalog").GetProperty("nodes").EnumerateArray()
            .First(n => n.GetProperty("node_id").GetString() == "nv-http-verify-1");
        Assert.Equal("1.1.6", node.GetProperty("server_version").GetString());
        Assert.Equal("fi-hel-01.nyxveil.ru", node.GetProperty("server_name").GetString());
    }

    private async Task RegisterCatalogNodeAsync()
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var nodes = scope.ServiceProvider.GetRequiredService<INodeRegistrationService>();
        var bootstrap = scope.ServiceProvider.GetRequiredService<IBootstrapTokenService>();

        await EnsurePlanLocationAsync(db);
        var locationId = await db.Locations.Where(l => l.Enabled).Select(l => l.LocationId).FirstAsync();
        if (await db.Nodes.AnyAsync(n => n.NodeId == "nv-http-verify-1"))
            return;

        var boot = await bootstrap.CreateAsync(new CreateBootstrapTokenRequest
        {
            ExpiresAt = DateTime.UtcNow.AddHours(1),
            MaxUses = 2,
            CreatedBy = "release-consumer"
        });

        await nodes.RegisterWithBootstrapAsync(new NodeRegisterRequest
        {
            BootstrapToken = boot.BootstrapToken,
            NodeId = "nv-http-verify-1",
            LocationId = locationId,
            DisplayName = "http-verify",
            PublicIdentity = RandomNumberGenerator.GetBytes(32),
            PublicKey = RandomNumberGenerator.GetBytes(32),
            ServerVersion = "1.1.6",
            ServerName = "fi-hel-01.nyxveil.ru",
            SpkiPin = RandomNumberGenerator.GetBytes(32),
            ProtocolVersion = 1,
            Capacity = 100,
            Endpoints =
            [
                new NodeEndpointDto
                {
                    Host = "fi-hel-01.nyxveil.ru", Port = 443, AddressFamily = "hostname", Priority = 1,
                    Enabled = true
                }
            ]
        });

        if (!await db.NodeTransports.AnyAsync(t => t.NodeId == "nv-http-verify-1"))
        {
            db.NodeTransports.Add(new NodeTransport
            {
                Id = Guid.NewGuid(), NodeId = "nv-http-verify-1", TransportType = "tls", Priority = 1, Enabled = true
            });
            db.NodeTransports.Add(new NodeTransport
            {
                Id = Guid.NewGuid(), NodeId = "nv-http-verify-1", TransportType = "quic", Priority = 2, Enabled = true
            });
            await db.SaveChangesAsync();
        }
    }

    private async Task<string> CreateLicenseTokenAsync()
    {
        using var scope = _factory.Services.CreateScope();
        var db = scope.ServiceProvider.GetRequiredService<ControlPlaneDbContext>();
        var licenses = scope.ServiceProvider.GetRequiredService<ILicenseProvisioningService>();
        var plan = await EnsurePlanLocationAsync(db);
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

    private static async Task<Plan> EnsurePlanLocationAsync(ControlPlaneDbContext db)
    {
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
        }

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
        return plan;
    }

    private static string LocateVerifySignedDir()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null)
        {
            foreach (var candidate in new[]
                     {
                         Path.Combine(dir.FullName, "tests", "CoreInterop", "verify-signed"),
                         Path.Combine(dir.FullName, "CoreInterop", "verify-signed")
                     })
            {
                if (Directory.Exists(candidate))
                    return candidate;
            }

            dir = dir.Parent;
        }

        return Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "..", "..", "CoreInterop", "verify-signed"));
    }

    private static string? FindGo()
    {
        foreach (var p in (Environment.GetEnvironmentVariable("PATH") ?? "").Split(Path.PathSeparator))
        {
            var candidate = Path.Combine(p, OperatingSystem.IsWindows() ? "go.exe" : "go");
            if (File.Exists(candidate))
                return candidate;
        }

        var tools = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            "tools", "go", "bin", OperatingSystem.IsWindows() ? "go.exe" : "go");
        return File.Exists(tools) ? tools : null;
    }
}
