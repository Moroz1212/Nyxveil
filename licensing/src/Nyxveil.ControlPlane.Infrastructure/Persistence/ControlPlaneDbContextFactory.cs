using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;
using Microsoft.Extensions.Configuration;

namespace Nyxveil.ControlPlane.Infrastructure.Persistence;

/// <summary>Design-time factory for EF Core migrations.</summary>
public sealed class ControlPlaneDbContextFactory : IDesignTimeDbContextFactory<ControlPlaneDbContext>
{
    public ControlPlaneDbContext CreateDbContext(string[] args)
    {
        var basePath = Directory.GetCurrentDirectory();
        var webPath = Path.GetFullPath(Path.Combine(basePath, "..", "Nyxveil.ControlPlane.Web"));
        if (!Directory.Exists(webPath))
            webPath = basePath;

        var config = new ConfigurationBuilder()
            .SetBasePath(webPath)
            .AddJsonFile("appsettings.json", optional: true)
            .AddJsonFile("appsettings.Development.json", optional: true)
            .AddEnvironmentVariables()
            .Build();

        var cs = config.GetConnectionString("ControlPlane")
                 ?? config["Database:ConnectionString"]
                 ?? "Server=(localdb)\\mssqllocaldb;Database=NyxveilControlPlane;Trusted_Connection=True;TrustServerCertificate=True";

        var options = new DbContextOptionsBuilder<ControlPlaneDbContext>()
            .UseSqlServer(cs)
            .Options;

        return new ControlPlaneDbContext(options);
    }
}
