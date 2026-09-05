namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>
/// License secret hashing matching Core store hmac1: verifiers.
/// </summary>
public interface ILicenseKeyHasher
{
    /// <summary>Creates a stored verifier of the form hmac1:&lt;hex&gt;.</summary>
    string CreateVerifier(string secret);

    /// <summary>Constant-time verify of candidate against stored verifier/plaintext/legacy.</summary>
    bool Verify(string stored, string candidate);
}
