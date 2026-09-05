namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// Validates node-authenticated requests (mTLS identity or signed headers).
/// </summary>
public interface INodeAuthenticator
{
    /// <param name="nodeId">Claimed node id.</param>
    /// <param name="signatureHeaders">HTTP signature / auth headers (name → value).</param>
    Task ValidateNodeRequestAsync(
        string nodeId,
        IReadOnlyDictionary<string, string> signatureHeaders,
        CancellationToken cancellationToken = default);
}
