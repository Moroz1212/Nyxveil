namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// Resolves the SQL connection string with DPAPI password overlay applied.
/// Never log the return value — it may contain credentials.
/// </summary>
public interface IDatabaseConnectionStringProvider
{
    /// <summary>Connection string for ControlPlane (password overlay applied when present).</summary>
    string GetConnectionString();
}
