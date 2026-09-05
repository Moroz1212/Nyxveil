using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.UnitTests;

/// <summary>
/// Mirrors installer port rules: 1..65535, default suggestion 8443 (Hosting is SoT).
/// </summary>
public class PortValidationTests
{
    public static bool IsValidPort(int port) => HostingOptions.IsValidPort(port);

    [Theory]
    [InlineData(1)]
    [InlineData(443)]
    [InlineData(8443)]
    [InlineData(9443)]
    [InlineData(10443)]
    [InlineData(65535)]
    public void Free_or_common_ports_are_accepted(int port) =>
        Assert.True(IsValidPort(port));

    [Theory]
    [InlineData(0)]
    [InlineData(-1)]
    [InlineData(65536)]
    [InlineData(int.MinValue)]
    public void Invalid_ports_are_rejected(int port) =>
        Assert.False(IsValidPort(port));

    [Fact]
    public void Installer_default_suggestion_is_8443_not_required()
    {
        Assert.Equal(8443, HostingOptions.DefaultPort);
        Assert.True(IsValidPort(HostingOptions.DefaultPort));
        Assert.True(IsValidPort(9443));
        Assert.True(HostingOptions.DefaultPort != 443);
    }
}

