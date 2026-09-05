using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class LicenseKeyHasherTests
{
    [Fact]
    public void TestLicenseVerifierWorks()
    {
        var hasher = new LicenseKeyHasher(Options.Create(new SecurityOptions
        {
            LicenseKekHex = ControlPlaneTestFixture.TestKekHex
        }));

        const string secret = "super-secret-license-material";
        var verifier = hasher.CreateVerifier(secret);

        Assert.StartsWith(LicenseKeyHasher.HmacPrefix, verifier);
        Assert.True(hasher.Verify(verifier, secret));
        Assert.False(hasher.Verify(verifier, "wrong-secret"));
        Assert.False(hasher.Verify(verifier, string.Empty));
        Assert.False(hasher.Verify(string.Empty, secret));
    }
}
