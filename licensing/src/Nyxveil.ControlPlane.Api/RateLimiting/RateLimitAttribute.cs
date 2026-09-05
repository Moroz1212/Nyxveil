namespace Nyxveil.ControlPlane.Api.RateLimiting;

/// <summary>
/// Marker for endpoints that should use the host's AspNetCore rate limiter.
/// Web host should call <c>AddRateLimiter</c> / <c>UseRateLimiting</c> and
/// apply <c>[EnableRateLimiting("default")]</c> or <c>[EnableRateLimiting("ticket")]</c>.
/// </summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Method, AllowMultiple = false, Inherited = true)]
public sealed class RateLimitAttribute : Attribute
{
    public const string DefaultPolicy = "default";
    public const string TicketPolicy = "ticket";

    public RateLimitAttribute(string policyName = DefaultPolicy)
    {
        PolicyName = policyName;
    }

    public string PolicyName { get; }
}
