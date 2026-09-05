using System.Security.Claims;
using System.Text.Encodings.Web;
using Microsoft.AspNetCore.Authentication;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Exceptions;

namespace Nyxveil.ControlPlane.Api.Auth;

/// <summary>
/// Optional ASP.NET authentication scheme for license Bearer / X-License-Token.
/// Prefer <see cref="LicenseAuthAttribute"/> on Control Plane v1 endpoints;
/// this handler is registered for hosts that want scheme-based auth.
/// </summary>
public sealed class LicenseTokenAuthHandler : AuthenticationHandler<AuthenticationSchemeOptions>
{
    public const string SchemeName = "LicenseToken";

    public LicenseTokenAuthHandler(
        IOptionsMonitor<AuthenticationSchemeOptions> options,
        ILoggerFactory logger,
        UrlEncoder encoder)
        : base(options, logger, encoder)
    {
    }

    protected override async Task<AuthenticateResult> HandleAuthenticateAsync()
    {
        var token = AuthTokenExtractor.ExtractLicenseOrBearerToken(Request);
        if (string.IsNullOrWhiteSpace(token))
            return AuthenticateResult.NoResult();

        try
        {
            if (AuthTokenExtractor.LooksLikeLicenseToken(token))
            {
                var licenses = Context.RequestServices.GetRequiredService<ILicenseProvisioningService>();
                var result = await licenses.ValidateLicenseTokenAsync(
                        new LicenseValidateRequest { LicenseToken = token },
                        Context.RequestAborted)
                    .ConfigureAwait(false);

                if (!result.Valid)
                    return AuthenticateResult.Fail("invalid license token");

                Context.Items[AuthContextKeys.LicenseToken] = token;
                Context.Items[AuthContextKeys.AuthKind] = AuthContextKeys.AuthKindLicense;

                var identity = new ClaimsIdentity(SchemeName);
                identity.AddClaim(new Claim("license_id", result.LicenseId ?? string.Empty));
                if (!string.IsNullOrEmpty(result.Plan))
                    identity.AddClaim(new Claim("plan", result.Plan));
                var principal = new ClaimsPrincipal(identity);
                return AuthenticateResult.Success(new AuthenticationTicket(principal, SchemeName));
            }

            var tickets = Context.RequestServices.GetRequiredService<IAccessTicketIssuer>();
            var claims = tickets.VerifyAccessTicket(token);
            Context.Items[AuthContextKeys.TicketClaims] = claims;
            Context.Items[AuthContextKeys.AuthKind] = AuthContextKeys.AuthKindTicket;

            var ticketIdentity = new ClaimsIdentity(SchemeName);
            ticketIdentity.AddClaim(new Claim("license_id", claims.LicenseId));
            ticketIdentity.AddClaim(new Claim("device_id", claims.DeviceId));
            ticketIdentity.AddClaim(new Claim(ClaimTypes.Role, claims.Role));
            var ticketPrincipal = new ClaimsPrincipal(ticketIdentity);
            return AuthenticateResult.Success(new AuthenticationTicket(ticketPrincipal, SchemeName));
        }
        catch (UnauthorizedException ex)
        {
            return AuthenticateResult.Fail(ex.Message);
        }
        catch (Exception ex)
        {
            return AuthenticateResult.Fail(ex);
        }
    }
}
