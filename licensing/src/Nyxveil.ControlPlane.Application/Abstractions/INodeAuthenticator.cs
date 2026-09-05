namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>Actual server request data; never populated from client method/path headers.</summary>
public sealed record NodeRequestData(string Method, string PathAndQuery, string BodySha256);

public interface INodeAuthenticator
{
    Task ValidateNodeRequestAsync(
        string nodeId,
        IReadOnlyDictionary<string, string> signatureHeaders,
        NodeRequestData request,
        CancellationToken cancellationToken = default);
}
