using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Security;

/// <summary>Privileged operator actions that require fresh step-up (and MFA for SuperAdmin).</summary>
public enum CriticalOperation
{
    DeleteNode = 1,
    RebootHost = 2,
    UpdateNode = 3,
    RestartService = 4,
    RollingUpdateStart = 5,
    ReconcileUnknownUpdate = 6,
    SigningKeyMutate = 7,
    SensitiveSettingsMutate = 8,
    AdminSecurityMutate = 9,
    ControlPlaneSelfUpdate = 10
}

public static class CriticalOperationPolicy
{
    public static bool RequiresStepUp(CriticalOperation op) => true;

    public static bool RequiresSuperAdmin(CriticalOperation op) => op switch
    {
        CriticalOperation.UpdateNode => true,
        CriticalOperation.RebootHost => true,
        CriticalOperation.RollingUpdateStart => true,
        CriticalOperation.ReconcileUnknownUpdate => true,
        CriticalOperation.SigningKeyMutate => true,
        CriticalOperation.SensitiveSettingsMutate => true,
        CriticalOperation.AdminSecurityMutate => true,
        CriticalOperation.ControlPlaneSelfUpdate => true,
        CriticalOperation.DeleteNode => true,
        CriticalOperation.RestartService => false, // OperatorOrAbove + step-up
        _ => true
    };

    public static CriticalOperation? ForNodeCommand(NodeCommandType type) => type switch
    {
        NodeCommandType.UpdateNodeLatest => CriticalOperation.UpdateNode,
        NodeCommandType.RebootHost => CriticalOperation.RebootHost,
        NodeCommandType.RestartNyxveilService => CriticalOperation.RestartService,
        NodeCommandType.RenewCertificate => null, // operational; role-gated only
        _ => null
    };

    /// <summary>Keys that mutate trust/auth/bootstrap/signing and require step-up.</summary>
    public static bool IsSensitiveSettingKey(string key)
    {
        if (string.IsNullOrWhiteSpace(key)) return false;
        var k = key.Trim();
        if (k.StartsWith("location_rollout:", StringComparison.OrdinalIgnoreCase))
            return true;
        return k.Contains("secret", StringComparison.OrdinalIgnoreCase)
               || k.Contains("token", StringComparison.OrdinalIgnoreCase)
               || k.Contains("bootstrap", StringComparison.OrdinalIgnoreCase)
               || k.Contains("signing", StringComparison.OrdinalIgnoreCase)
               || k.Contains("auth", StringComparison.OrdinalIgnoreCase)
               || k.Contains("mfa", StringComparison.OrdinalIgnoreCase)
               || k.Contains("trust", StringComparison.OrdinalIgnoreCase)
               || k.Contains("kek", StringComparison.OrdinalIgnoreCase)
               || k.Contains("password", StringComparison.OrdinalIgnoreCase);
    }
}

/// <summary>
/// Server-side gate for critical ops. UI checks are complementary only.
/// </summary>
public interface ICriticalOperationAuthorizer
{
    void AssertAllowed(CriticalOperation operation, IEnumerable<string>? roles = null);
}

/// <summary>
/// Marks the current async flow as an authorized location-rollout continuation
/// so subsequent UpdateNode enqueues do not re-demand step-up.
/// </summary>
public static class RolloutContinuationScope
{
    private static readonly AsyncLocal<bool> Active = new();

    public static bool IsActive => Active.Value;

    public static IDisposable Begin()
    {
        var previous = Active.Value;
        Active.Value = true;
        return new Pop(previous);
    }

    private sealed class Pop(bool previous) : IDisposable
    {
        public void Dispose() => Active.Value = previous;
    }
}

/// <summary>Default for unit tests / non-HTTP hosts: no elevation gate.</summary>
public sealed class AllowAllCriticalOperationAuthorizer : ICriticalOperationAuthorizer
{
    public static AllowAllCriticalOperationAuthorizer Instance { get; } = new();

    public void AssertAllowed(CriticalOperation operation, IEnumerable<string>? roles = null)
    {
        _ = operation;
        _ = roles;
    }
}

/// <summary>Worker host: only rollout-continuation UpdateNode is allowed without HTTP step-up.</summary>
public sealed class DenyUnlessRolloutCriticalOperationAuthorizer : ICriticalOperationAuthorizer
{
    public void AssertAllowed(CriticalOperation operation, IEnumerable<string>? roles = null)
    {
        if (operation == CriticalOperation.UpdateNode && RolloutContinuationScope.IsActive)
            return;
        throw new ForbiddenException("step-up authentication required");
    }
}
