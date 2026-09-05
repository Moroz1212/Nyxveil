using Microsoft.Extensions.Options;

namespace Nyxveil.ControlPlane.Application.Options;

public sealed class HostingOptionsValidator : IValidateOptions<HostingOptions>
{
    public ValidateOptionsResult Validate(string? name, HostingOptions options)
    {
        if (options.Port != 0 && !HostingOptions.IsValidPort(options.Port))
        {
            return ValidateOptionsResult.Fail(
                $"Hosting:Port must be 1-65535 (got {options.Port}). Port 0 is reserved for test hosts that skip binding.");
        }

        if (string.IsNullOrWhiteSpace(options.BindAddress))
            return ValidateOptionsResult.Fail("Hosting:BindAddress is required.");

        return ValidateOptionsResult.Success;
    }
}
