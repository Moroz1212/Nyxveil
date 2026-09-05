using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Application;

public static class DependencyInjection
{
    /// <summary>
    /// Registers Application-layer pure services. Bind options with
    /// <see cref="AddApplication(IServiceCollection, IConfiguration)"/> or
    /// <see cref="AddApplicationOptions"/>.
    /// Concrete Infrastructure implementations are registered separately.
    /// </summary>
    public static IServiceCollection AddApplication(this IServiceCollection services)
    {
        services.AddSingleton<IClock, SystemClock>();
        return services;
    }

    public static IServiceCollection AddApplication(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddApplicationOptions(configuration);
        return services.AddApplication();
    }

    public static IServiceCollection AddApplicationOptions(this IServiceCollection services, IConfiguration configuration)
    {
        ArgumentNullException.ThrowIfNull(configuration);

        services.AddSingleton<IValidateOptions<HostingOptions>, HostingOptionsValidator>();
        services.AddOptions<HostingOptions>()
            .Bind(configuration.GetSection(HostingOptions.SectionName))
            .ValidateOnStart();

        services.Configure<DatabaseOptions>(configuration.GetSection(DatabaseOptions.SectionName));
        services.Configure<HttpsOptions>(configuration.GetSection(HttpsOptions.SectionName));
        services.Configure<CertificateOptions>(configuration.GetSection(CertificateOptions.SectionName));
        services.Configure<SetupOptions>(configuration.GetSection(SetupOptions.SectionName));
        services.Configure<SigningOptions>(configuration.GetSection(SigningOptions.SectionName));
        services.Configure<NodeAuthOptions>(configuration.GetSection(NodeAuthOptions.SectionName));
        services.Configure<TicketOptions>(configuration.GetSection(TicketOptions.SectionName));
        services.Configure<NodeHeartbeatOptions>(configuration.GetSection(NodeHeartbeatOptions.SectionName));
        services.Configure<MetricsRetentionOptions>(configuration.GetSection(MetricsRetentionOptions.SectionName));
        services.Configure<RateLimitOptions>(configuration.GetSection(RateLimitOptions.SectionName));
        services.Configure<SecurityOptions>(configuration.GetSection(SecurityOptions.SectionName));
        services.Configure<UiOptions>(configuration.GetSection(UiOptions.SectionName));
        services.Configure<FileLoggingOptions>(configuration.GetSection(FileLoggingOptions.SectionName));
        return services;
    }

    /// <summary>Alias used by hosts that prefer the DependencyInjection folder naming.</summary>
    public static IServiceCollection AddControlPlaneOptions(
        this IServiceCollection services,
        IConfiguration configuration) =>
        services.AddApplicationOptions(configuration);
}
