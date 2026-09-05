namespace Nyxveil.ControlPlane.Domain.Common;

/// <summary>
/// Base for Guid-keyed domain entities. All DateTime values use UTC (DateTimeKind.Utc).
/// </summary>
public abstract class EntityBase
{
    public Guid Id { get; set; }
}
