using Nyxveil.ControlPlane.Application.Common;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class DashboardExpiryTests
{
    private static readonly DateTime Now = new(2026, 9, 6, 12, 0, 0, DateTimeKind.Utc);

    [Theory]
    [InlineData(-1, CertificateHealthStatus.Expired)]
    [InlineData(7, CertificateHealthStatus.Critical)]
    [InlineData(20, CertificateHealthStatus.ExpiringSoon)]
    [InlineData(45, CertificateHealthStatus.Healthy)]
    public void CertificateThresholdsAreCentralized(int days, CertificateHealthStatus expected)
    {
        Assert.Equal(expected, CertificateExpiry.Evaluate(Now.AddDays(days), Now));
    }

    [Fact]
    public void MissingCertificateIsUnknown()
    {
        Assert.Equal(CertificateHealthStatus.Unknown, CertificateExpiry.Evaluate(null, Now));
        Assert.Null(CertificateExpiry.DaysRemaining(null, Now));
    }
}
