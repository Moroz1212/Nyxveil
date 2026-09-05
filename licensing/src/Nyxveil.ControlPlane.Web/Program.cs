using System.Net;
using System.Security.Cryptography.X509Certificates;
using System.Threading.RateLimiting;
using Microsoft.AspNetCore.Components.Authorization;
using Microsoft.AspNetCore.Diagnostics.HealthChecks;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.RateLimiting;
using Microsoft.Extensions.Diagnostics.HealthChecks;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Api.DependencyInjection;
using Nyxveil.ControlPlane.Application;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Configuration;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;
using Nyxveil.ControlPlane.Infrastructure.Hosting;
using Nyxveil.ControlPlane.Infrastructure.Identity;
using Nyxveil.ControlPlane.Infrastructure.Logging;
using Nyxveil.ControlPlane.Web.Cli;
using Nyxveil.ControlPlane.Web.Components;
using Nyxveil.ControlPlane.Web.Health;
using Nyxveil.ControlPlane.Web.Hubs;
using Nyxveil.ControlPlane.Web.Security;
using Nyxveil.ControlPlane.Worker.DependencyInjection;

// CLI commands (before web host) — installer / ops use these.
if (args.Length > 0)
{
    var exit = await TryRunCliAsync(args).ConfigureAwait(false);
    if (exit is not null)
        return exit.Value;
}

var builder = WebApplication.CreateBuilder(args);

builder.Host.UseWindowsService(options =>
{
    options.ServiceName = "NyxveilControlPlane";
});

if (builder.Environment.IsProduction())
{
    var programData = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
        "Nyxveil",
        "ControlPlane");
    builder.Configuration.AddNyxveilProtectedSecrets(programData);
}

ConfigureFileLogging(builder);

X509Certificate2? httpsCertificate = null;
ConfigureKestrelHttps(builder, ref httpsCertificate);
HttpsEnforcement.EnforceProductionHttps(builder.Environment, builder.Configuration, httpsCertificate);

builder.Services.AddApplication(builder.Configuration);
builder.Services.AddInfrastructure(builder.Configuration);
builder.Services.AddControlPlaneApi(builder.Configuration);
builder.Services.AddControlPlaneWorkers();

var authBuilder = builder.Services.AddAuthentication(IdentityConstants.ApplicationScheme);
authBuilder.AddIdentityCookies();
authBuilder.AddControlPlaneLicenseAuth();

builder.Services.ConfigureApplicationCookie(options =>
{
    options.Cookie.Name = "Nyxveil.ControlPlane.Auth";
    options.Cookie.HttpOnly = true;
    options.Cookie.SecurePolicy = CookieSecurePolicy.Always;
    options.Cookie.SameSite = SameSiteMode.Lax;
    options.LoginPath = "/account/login";
    options.AccessDeniedPath = "/account/access-denied";
    options.SlidingExpiration = true;
    options.ExpireTimeSpan = TimeSpan.FromHours(8);
});

builder.Services.AddAuthorization(options =>
{
    options.AddPolicy(AuthPolicies.AnyAdmin, policy =>
        policy.RequireRole(AdminRole.SuperAdmin, AdminRole.Operator, AdminRole.ReadOnly));
    options.AddPolicy(AuthPolicies.OperatorOrAbove, policy =>
        policy.RequireRole(AdminRole.SuperAdmin, AdminRole.Operator));
    options.AddPolicy(AuthPolicies.SuperAdminOnly, policy =>
        policy.RequireRole(AdminRole.SuperAdmin));
});

builder.Services.AddCascadingAuthenticationState();
builder.Services.AddScoped<AuthenticationStateProvider, IdentityRevalidatingAuthenticationStateProvider>();
builder.Services.AddHttpContextAccessor();

builder.Services.AddRazorComponents()
    .AddInteractiveServerComponents();

builder.Services.AddEndpointsApiExplorer();
if (builder.Environment.IsDevelopment())
{
    builder.Services.AddSwaggerGen(options =>
    {
        options.SwaggerDoc("v1", new() { Title = "Nyxveil Control Plane API", Version = "v1" });
    });
}

builder.Services.AddSignalR();

var rateLimits = builder.Configuration.GetSection(RateLimitOptions.SectionName).Get<RateLimitOptions>()
                 ?? new RateLimitOptions();

builder.Services.AddRateLimiter(options =>
{
    options.RejectionStatusCode = StatusCodes.Status429TooManyRequests;
    options.OnRejected = async (context, token) =>
    {
        context.HttpContext.Response.ContentType = "application/json";
        await context.HttpContext.Response.WriteAsync(
            """{"error":"rate_limited","message":"Too many requests"}""",
            token).ConfigureAwait(false);
    };

    options.AddPolicy("api-sensitive", httpContext =>
    {
        var path = httpContext.Request.Path.Value ?? string.Empty;
        if (path.Contains("/heartbeat", StringComparison.OrdinalIgnoreCase) ||
            path.Contains("/nodes/heartbeat", StringComparison.OrdinalIgnoreCase))
        {
            return RateLimitPartition.GetNoLimiter("heartbeat");
        }

        var isTicket = path.Contains("/ticket/", StringComparison.OrdinalIgnoreCase);
        var permit = isTicket ? rateLimits.TicketPermitLimit : rateLimits.PermitLimit;
        var windowSeconds = isTicket ? rateLimits.TicketWindowSeconds : rateLimits.WindowSeconds;
        var partitionKey = httpContext.Connection.RemoteIpAddress?.ToString() ?? "unknown";

        return RateLimitPartition.GetFixedWindowLimiter(
            partitionKey,
            _ => new FixedWindowRateLimiterOptions
            {
                PermitLimit = Math.Max(1, permit),
                Window = TimeSpan.FromSeconds(Math.Max(1, windowSeconds)),
                QueueLimit = Math.Max(0, rateLimits.Burst)
            });
    });
});

builder.Services.AddHealthChecks()
    .AddCheck("self", () => HealthCheckResult.Healthy(), tags: ["live"])
    .AddSqlServer(
        sp => sp.GetRequiredService<IDatabaseConnectionStringProvider>().GetConnectionString(),
        name: "sqlserver",
        tags: ["ready"])
    .AddCheck<SigningKeyHealthCheck>("signing_key", tags: ["ready"]);

var app = builder.Build();

await SeedRolesAsync(app.Services).ConfigureAwait(false);

if (app.Environment.IsDevelopment())
{
    app.UseSwagger();
    app.UseSwaggerUI();
}
else
{
    // Production: never return stack traces to clients.
    app.UseExceptionHandler(exceptionApp =>
    {
        exceptionApp.Run(async context =>
        {
            context.Response.StatusCode = StatusCodes.Status500InternalServerError;
            context.Response.ContentType = "application/problem+json";
            var problem = new
            {
                title = "An error occurred while processing your request.",
                status = 500,
                traceId = context.TraceIdentifier
            };
            await context.Response.WriteAsJsonAsync(problem).ConfigureAwait(false);
        });
    });
    app.UseHsts();
}

app.UseStatusCodePagesWithReExecute("/not-found", createScopeForStatusCodePages: true);

if (!app.Environment.IsDevelopment())
{
    app.UseHttpsRedirection();
}

app.UseAuthentication();
app.UseAuthorization();
app.UseRateLimiter();
app.UseAntiforgery();

app.MapStaticAssets();

app.MapHealthChecks("/health/live", new HealthCheckOptions
{
    Predicate = check => check.Tags.Contains("live")
});

app.MapHealthChecks("/health/ready", new HealthCheckOptions
{
    Predicate = check => check.Tags.Contains("ready"),
    ResponseWriter = async (context, report) =>
    {
        context.Response.ContentType = "application/json";
        var payload = new
        {
            status = report.Status.ToString(),
            checks = report.Entries.Select(e => new
            {
                name = e.Key,
                status = e.Value.Status.ToString()
            })
        };
        await context.Response.WriteAsJsonAsync(payload).ConfigureAwait(false);
    }
});

app.MapControllers().RequireRateLimiting("api-sensitive");
app.MapHub<NodeStatusHub>("/hubs/node-status");

PreferMinimalApiOverBlazor(
    app.MapPost("/account/login", async (
        HttpContext http,
        SignInManager<ApplicationUser> signInManager,
        UserManager<ApplicationUser> userManager,
        IAuditService audit) =>
    {
        var form = await http.Request.ReadFormAsync().ConfigureAwait(false);
        var email = form["email"].ToString();
        var password = form["password"].ToString();
        var returnUrl = form["returnUrl"].ToString();
        if (string.IsNullOrWhiteSpace(returnUrl) || !returnUrl.StartsWith('/'))
        {
            returnUrl = "/";
        }

        var user = await userManager.FindByEmailAsync(email).ConfigureAwait(false);
        if (user is null)
        {
            return Results.Redirect($"/account/login?error=1&returnUrl={Uri.EscapeDataString(returnUrl)}");
        }

        var result = await signInManager.PasswordSignInAsync(user, password, isPersistent: true, lockoutOnFailure: true)
            .ConfigureAwait(false);
        if (!result.Succeeded)
        {
            return Results.Redirect($"/account/login?error=1&returnUrl={Uri.EscapeDataString(returnUrl)}");
        }

        await audit.WriteAsync(new()
        {
            Actor = user.Email ?? user.UserName ?? user.Id,
            Action = "admin.login",
            EntityType = "AdminUser",
            EntityId = user.Id,
            IpAddress = http.Connection.RemoteIpAddress?.ToString()
        }).ConfigureAwait(false);

        return Results.Redirect(returnUrl);
    }).DisableAntiforgery().RequireRateLimiting("api-sensitive"));

PreferMinimalApiOverBlazor(
    app.MapPost("/account/logout", async (SignInManager<ApplicationUser> signInManager) =>
    {
        await signInManager.SignOutAsync().ConfigureAwait(false);
        return Results.Redirect("/account/login");
    }).DisableAntiforgery().RequireAuthorization());

PreferMinimalApiOverBlazor(
    app.MapPost("/account/setup", async (
        HttpContext http,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole> roleManager,
        IAuditService audit,
        IConfiguration configuration,
        IHostEnvironment environment) =>
    {
        await RoleSeeder.EnsureRolesAsync(roleManager).ConfigureAwait(false);

        var admins = await userManager.GetUsersInRoleAsync(AdminRole.SuperAdmin).ConfigureAwait(false);
        var setupOptions = WebBootstrapGuard.ReadSetupOptions(configuration);

        var form = await http.Request.ReadFormAsync().ConfigureAwait(false);
        var providedToken = form[WebBootstrapGuard.BootstrapTokenFormField].ToString();
        if (string.IsNullOrEmpty(providedToken))
            providedToken = WebBootstrapGuard.ExtractToken(http) ?? string.Empty;

        if (!WebBootstrapGuard.IsWebBootstrapAllowed(
                http, environment, setupOptions, admins.Count > 0, providedToken))
        {
            if (!setupOptions.AllowWebBootstrap || admins.Count > 0)
                return Results.NotFound();
            return Results.StatusCode(StatusCodes.Status403Forbidden);
        }

        var email = form["email"].ToString().Trim();
        var password = form["password"].ToString();
        var displayName = form["displayName"].ToString().Trim();

        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            EmailConfirmed = true,
            DisplayName = string.IsNullOrWhiteSpace(displayName) ? email : displayName
        };

        var create = await userManager.CreateAsync(user, password).ConfigureAwait(false);
        if (!create.Succeeded)
        {
            var msg = string.Join("; ", create.Errors.Select(e => e.Description));
            return Results.Redirect($"/setup?error={Uri.EscapeDataString(msg)}");
        }

        await userManager.AddToRoleAsync(user, AdminRole.SuperAdmin).ConfigureAwait(false);
        await audit.WriteAsync(new()
        {
            Actor = email,
            Action = "admin.setup",
            EntityType = "AdminUser",
            EntityId = user.Id,
            Detail = "First SuperAdmin created",
            IpAddress = http.Connection.RemoteIpAddress?.ToString()
        }).ConfigureAwait(false);

        return Results.Redirect("/account/login?setup=1");
    }).DisableAntiforgery().RequireRateLimiting("api-sensitive"));

app.MapRazorComponents<App>()
    .AddInteractiveServerRenderMode();

app.Run();
return 0;

static async Task<int?> TryRunCliAsync(string[] args)
{
    var cmd = args[0];

    if (cmd.Equals("admin", StringComparison.OrdinalIgnoreCase) && args.Length > 1)
    {
        if (args[1].Equals("create", StringComparison.OrdinalIgnoreCase))
            return await AdminCreateCommand.RunAsync(args).ConfigureAwait(false);
        if (args[1].Equals("reset-password", StringComparison.OrdinalIgnoreCase))
            return await AdminResetPasswordCommand.RunAsync(args).ConfigureAwait(false);
    }

    if (cmd.Equals("self-test-http", StringComparison.OrdinalIgnoreCase))
        return await SelfTestCommand.RunAsync(args, probeTls: true).ConfigureAwait(false);

    if (cmd.Equals("self-test", StringComparison.OrdinalIgnoreCase))
    {
        var tls = args.Any(a => a.Equals("--tls", StringComparison.OrdinalIgnoreCase));
        return await SelfTestCommand.RunAsync(args, probeTls: tls).ConfigureAwait(false);
    }

    if (cmd.Equals("backup-signing-keys", StringComparison.OrdinalIgnoreCase))
        return await BackupSigningKeysCommand.RunAsync(args).ConfigureAwait(false);

    if (cmd.Equals("backup-recovery", StringComparison.OrdinalIgnoreCase))
        return await RecoveryBundleCommand.RunBackupAsync(args).ConfigureAwait(false);

    if (cmd.Equals("restore-recovery", StringComparison.OrdinalIgnoreCase))
        return await RecoveryBundleCommand.RunRestoreAsync(args).ConfigureAwait(false);

    if (cmd.Equals("secrets", StringComparison.OrdinalIgnoreCase) &&
        args.Length > 1 &&
        args[1].Equals("ensure-kek", StringComparison.OrdinalIgnoreCase))
    {
        return SecretsEnsureKekCommand.Run();
    }

    if (cmd.Equals("certificate", StringComparison.OrdinalIgnoreCase) &&
        args.Length > 1 &&
        args[1].Equals("validate", StringComparison.OrdinalIgnoreCase))
    {
        return CertificateValidateCommand.Run(args);
    }

    return null;
}

static void PreferMinimalApiOverBlazor(IEndpointConventionBuilder endpoint) =>
    endpoint.Add(b =>
    {
        if (b is RouteEndpointBuilder route)
            route.Order = -1;
    });

static void ConfigureFileLogging(WebApplicationBuilder builder)
{
    builder.Services.Configure<FileLoggingOptions>(builder.Configuration.GetSection(FileLoggingOptions.SectionName));
    var opts = builder.Configuration.GetSection(FileLoggingOptions.SectionName).Get<FileLoggingOptions>();
    if (opts?.Enabled == true)
        builder.Services.AddSingleton<ILoggerProvider, RotatingFileLoggerProvider>();
}

/// <summary>
/// Single HTTPS listen via Hosting:BindAddress + Hosting:Port.
/// When Hosting.Port &gt; 0, Kestrel:Endpoints from configuration are cleared so they cannot dual-listen.
/// Hosting is the source of truth for listen address/port.
/// </summary>
static void ConfigureKestrelHttps(WebApplicationBuilder builder, ref X509Certificate2? httpsCertificate)
{
    var hosting = builder.Configuration.GetSection(HostingOptions.SectionName).Get<HostingOptions>()
                  ?? new HostingOptions();

    if (hosting.Port != 0 && !HostingOptions.IsValidPort(hosting.Port))
    {
        throw new InvalidOperationException(
            $"Hosting:Port must be 1-65535 (got {hosting.Port}).");
    }

    if (hosting.Port > 0)
        ClearKestrelEndpoints(builder);

    var certOptions = CertificateLoader.Bind(builder.Configuration);
    var isProduction = builder.Environment.IsProduction();

    var shouldBind =
        isProduction
        || certOptions.Mode == CertificateMode.SelfSigned
        || !string.IsNullOrWhiteSpace(certOptions.Thumbprint)
        || !string.IsNullOrWhiteSpace(certOptions.PfxPath);

    if (!shouldBind)
        return;

    if (!CertificateLoader.TryLoad(
            certOptions,
            hosting.PublicHostname,
            isProduction,
            out var cert,
            out var error) || cert is null)
    {
        if (isProduction)
            return;

        _ = error;
        return;
    }

    httpsCertificate = cert;
    if (!IPAddress.TryParse(hosting.BindAddress, out var ip))
        ip = IPAddress.Any;

    var port = hosting.Port <= 0 ? HostingOptions.DefaultPort : hosting.Port;
    var captured = cert;

    builder.WebHost.ConfigureKestrel(options =>
    {
        options.Listen(ip, port, listen => listen.UseHttps(captured));
    });
}

/// <summary>
/// Nulls out Kestrel:Endpoints keys from older installers so only ConfigureKestrel Listen is used.
/// </summary>
static void ClearKestrelEndpoints(WebApplicationBuilder builder)
{
    builder.Configuration.AddInMemoryCollection(new Dictionary<string, string?>
    {
        ["Kestrel:Endpoints:Https:Url"] = null,
        ["Kestrel:Endpoints:Https:Certificate:Thumbprint"] = null,
        ["Kestrel:Endpoints:Https:Certificate:Subject"] = null,
        ["Kestrel:Endpoints:Https:Certificate:Store"] = null,
        ["Kestrel:Endpoints:Https:Certificate:Location"] = null,
        ["Kestrel:Endpoints:Http:Url"] = null
    });
}

static async Task SeedRolesAsync(IServiceProvider services)
{
    using var scope = services.CreateScope();
    await RoleSeeder.EnsureRolesAsync(scope.ServiceProvider).ConfigureAwait(false);
}

public partial class Program;
