using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Worker.HostedServices;

namespace Nyxveil.ControlPlane.Worker.DependencyInjection;

public static class ServiceCollectionExtensions
{
    public static IServiceCollection AddControlPlaneWorkers(this IServiceCollection services)
    {
        services.AddHostedService<NodeHealthEvaluationWorker>();
        services.AddHostedService<LicenseExpirationWorker>();
        services.AddHostedService<MetricsRetentionWorker>();
        services.AddHostedService<RevocationSnapshotWorker>();
        return services;
    }
}
