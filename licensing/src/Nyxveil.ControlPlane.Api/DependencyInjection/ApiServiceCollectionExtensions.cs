using Microsoft.AspNetCore.Authentication;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Api.Auth;
using Nyxveil.ControlPlane.Api.Filters;
using Nyxveil.ControlPlane.Application.Serialization;

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
            .AddJsonOptions(options =>
            {
                // External API timestamps must be unambiguous UTC with trailing Z.
                options.JsonSerializerOptions.Converters.Add(new UtcDateTimeJsonConverter());
                options.JsonSerializerOptions.Converters.Add(new UtcNullableDateTimeJsonConverter());
                options.JsonSerializerOptions.Converters.Add(new UtcDateTimeOffsetJsonConverter());
                options.JsonSerializerOptions.Converters.Add(new UtcNullableDateTimeOffsetJsonConverter());
            })
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
