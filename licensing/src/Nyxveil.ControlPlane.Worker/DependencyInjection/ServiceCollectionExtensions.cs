using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Nyxveil.ControlPlane.Application.Security;
using Nyxveil.ControlPlane.Worker.HostedServices;

namespace Nyxveil.ControlPlane.Worker.DependencyInjection;

public static class ServiceCollectionExtensions
{
    public static IServiceCollection AddControlPlaneWorkers(this IServiceCollection services)
    {
        services.RemoveAll<ICriticalOperationAuthorizer>();
        services.AddSingleton<ICriticalOperationAuthorizer, DenyUnlessRolloutCriticalOperationAuthorizer>();
        services.AddHostedService<NodeHealthEvaluationWorker>();
        services.AddHostedService<LicenseExpirationWorker>();
        services.AddHostedService<MetricsRetentionWorker>();
        services.AddHostedService<RevocationSnapshotWorker>();
        services.AddHostedService<SigningKeyRetirementWorker>();
        services.AddHostedService<LocationRolloutWorker>();
        return services;
    }
}
