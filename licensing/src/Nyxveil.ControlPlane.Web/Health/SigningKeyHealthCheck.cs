using Microsoft.Extensions.Diagnostics.HealthChecks;
using Nyxveil.ControlPlane.Application.Abstractions;

namespace Nyxveil.ControlPlane.Web.Health;

public sealed class SigningKeyHealthCheck : IHealthCheck
{
    private readonly ISigningKeyService _signingKeys;

    public SigningKeyHealthCheck(ISigningKeyService signingKeys)
    {
        _signingKeys = signingKeys;
    }

    public async Task<HealthCheckResult> CheckHealthAsync(
        HealthCheckContext context,
        CancellationToken cancellationToken = default)
    {
        try
        {
            var material = await _signingKeys.GetCurrentSigningMaterialAsync(cancellationToken)
                .ConfigureAwait(false);
            if (string.IsNullOrWhiteSpace(material.KeyId) || material.PublicKey.Length == 0)
            {
                return HealthCheckResult.Unhealthy("Signing key material unavailable");
            }

            return HealthCheckResult.Healthy($"key_id={material.KeyId}");
        }
        catch (Exception ex)
        {
            return HealthCheckResult.Unhealthy("Signing key check failed", ex);
        }
    }
}
