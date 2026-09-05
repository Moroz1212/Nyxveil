namespace Nyxveil.ControlPlane.Domain.Entities;

public sealed class NodeRequestNonce
{
    public string NodeId { get; set; } = string.Empty;
    public string NonceHash { get; set; } = string.Empty;
    public DateTime Timestamp { get; set; }
    public DateTime ExpiresAt { get; set; }
}
