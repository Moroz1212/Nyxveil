using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class UpdatePreflightMfaUxTests
{
    [Fact]
    public void UpdatePreflightDialog_KeepsModal_WhenStepUpRequired()
    {
        var root = FindLicensingRoot();
        var dialog = File.ReadAllText(Path.Combine(root,
            "src", "Nyxveil.ControlPlane.Web", "Components", "Shared", "UpdatePreflightDialog.razor"));
        Assert.Contains("StepUpRequired", dialog, StringComparison.Ordinal);
        Assert.Contains("Подтвердить MFA", dialog, StringComparison.Ordinal);
        Assert.Contains("disabled=\"@(!Result.CanEnqueue || Busy || StepUpRequired)\"", dialog, StringComparison.Ordinal);
        // Must not auto-close modal from Confirm before parent decides.
        Assert.DoesNotContain("await VisibleChanged.InvokeAsync(false);\r\n        await OnConfirm", dialog, StringComparison.Ordinal);

        var node = File.ReadAllText(Path.Combine(root,
            "src", "Nyxveil.ControlPlane.Web", "Components", "Pages", "Admin", "NodeDetails.razor"));
        Assert.Contains("StepUpRequired=\"@StepUpGuard.RequiresStepUp", node, StringComparison.Ordinal);
        Assert.Contains("Keep preflight open when step-up expired", node, StringComparison.Ordinal);
    }

    private static string FindLicensingRoot()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null)
        {
            if (File.Exists(Path.Combine(dir.FullName, "VERSION")) &&
                Directory.Exists(Path.Combine(dir.FullName, "src", "Nyxveil.ControlPlane.Web")))
                return dir.FullName;
            dir = dir.Parent;
        }
        throw new DirectoryNotFoundException("licensing root not found");
    }
}
