using Microsoft.AspNetCore.Identity;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Identity;

namespace Nyxveil.ControlPlane.Web.Security;

/// <summary>
/// Forces SuperAdmin accounts to complete authenticator MFA before using the panel.
/// </summary>
public sealed class MfaEnforcementMiddleware
{
    private readonly RequestDelegate _next;

    public MfaEnforcementMiddleware(RequestDelegate next) => _next = next;

    public async Task InvokeAsync(HttpContext context, UserManager<ApplicationUser> userManager)
    {
        if (context.User.Identity?.IsAuthenticated == true
            && context.User.IsInRole(AdminRole.SuperAdmin)
            && !MfaPathRules.IsExempt(context.Request.Path))
        {
            var user = await userManager.GetUserAsync(context.User).ConfigureAwait(false);
            if (user is not null && !await userManager.GetTwoFactorEnabledAsync(user).ConfigureAwait(false))
            {
                var returnUrl = context.Request.PathBase + context.Request.Path + context.Request.QueryString;
                var target = "/account/mfa/setup?required=1";
                if (!string.IsNullOrEmpty(returnUrl) && returnUrl.StartsWith('/'))
                    target += "&returnUrl=" + Uri.EscapeDataString(returnUrl);

                context.Response.Redirect(target);
                return;
            }
        }

        await _next(context).ConfigureAwait(false);
    }
}
