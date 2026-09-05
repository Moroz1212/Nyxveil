using Microsoft.AspNetCore.Authentication;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.Filters;

namespace Nyxveil.ControlPlane.Api.DependencyInjection;

public static class ApiServiceCollectionExtensions
{
    /// <summary>
    /// Registers Control Plane HTTP API controllers from this assembly and
    /// exception→ProblemDetails mapping. Authentication schemes are composed by the Web host.
    /// </summary>
    public static IServiceCollection AddControlPlaneApi(
        this IServiceCollection services,
        IConfiguration? configuration = null)
    {
        _ = configuration;

        services
            .AddControllers()
            .AddApplicationPart(typeof(ApiServiceCollectionExtensions).Assembly)
            .AddMvcOptions(options =>
            {
                options.Filters.Add<ApplicationExceptionFilter>();
            });

        services.AddProblemDetails();

        return services;
    }

    /// <summary>Adds the LicenseToken authentication scheme (call after host cookie auth is configured).</summary>
    public static AuthenticationBuilder AddControlPlaneLicenseAuth(this AuthenticationBuilder builder) =>
        builder.AddScheme<AuthenticationSchemeOptions, LicenseTokenAuthHandler>(
            LicenseTokenAuthHandler.SchemeName,
            _ => { });
}
