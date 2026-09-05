namespace Nyxveil.ControlPlane.Application.Options;

public sealed class NodeAuthOptions
{
    public const string SectionName = "NodeAuth";

    /// <summary>
    /// Legacy credential issuance compatibility only; disabled by default.
    /// Normal Node API always requires req-v2, regardless of this option.
    /// </summary>
    public bool AllowLegacyBearer { get; set; }
}
