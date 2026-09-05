using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Infrastructure.Identity;

public static class RoleSeeder
{
    public static readonly string[] AllRoles =
    {
        AdminRole.SuperAdmin,
        AdminRole.Operator,
        AdminRole.ReadOnly
    };

    public static async Task EnsureRolesAsync(RoleManager<IdentityRole> roleManager, CancellationToken cancellationToken = default)
    {
        foreach (var role in AllRoles)
        {
            if (!await roleManager.RoleExistsAsync(role).ConfigureAwait(false))
            {
                var result = await roleManager.CreateAsync(new IdentityRole(role)).ConfigureAwait(false);
                if (!result.Succeeded)
                {
                    var errors = string.Join("; ", result.Errors.Select(e => e.Description));
                    throw new InvalidOperationException($"Failed to create role '{role}': {errors}");
                }
            }
        }
    }

    public static async Task EnsureRolesAsync(IServiceProvider services, CancellationToken cancellationToken = default)
    {
        using var scope = services.CreateScope();
        var roleManager = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole>>();
        await EnsureRolesAsync(roleManager, cancellationToken).ConfigureAwait(false);
    }
}
