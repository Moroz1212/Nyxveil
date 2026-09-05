using Microsoft.AspNetCore.Identity;

namespace Nyxveil.ControlPlane.Infrastructure.Identity;

public class ApplicationUser : IdentityUser
{
    public string? DisplayName { get; set; }
}
