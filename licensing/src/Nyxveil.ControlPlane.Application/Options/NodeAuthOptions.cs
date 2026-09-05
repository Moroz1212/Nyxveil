namespace Nyxveil.ControlPlane.Application.Options;

public sealed class NodeAuthOptions
{
    public const string SectionName = "NodeAuth";

    /// <summary>
    /// When true, accept long-lived <c>nvpnode_&lt;id&gt;_&lt;secret&gt;</c> bearers.
    /// Production default is false — prefer Frozen Core <c>nvp-node-v1</c> tokens or request signatures.
    /// </summary>
    public bool AllowLegacyBearer { get; set; }
}
