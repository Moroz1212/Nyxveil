using System.Reflection;
using System.Text;
using System.Text.Json;
using Nyxveil.ControlPlane.Application.Common;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Services;
using Nyxveil.ControlPlane.Web.Presentation;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class Utf8EncodingRegressionTests
{
    public static TheoryData<string> CriticalRuStrings => new()
    {
        AttentionCopy.OperationFailed,
        AttentionCopy.UnknownUpdateOutcome,
        AttentionCopy.RenewCertificate,
        AttentionCopy.PanelTitle,
        AttentionCopy.ServerOffline,
        AttentionCopy.NoData,
        "ёЁ—→№"
    };

    [Theory]
    [MemberData(nameof(CriticalRuStrings))]
    public void CriticalRussian_IsValidUnicodeNotMojibake(string value)
    {
        Assert.False(string.IsNullOrWhiteSpace(value));
        Assert.False(LooksLikeUtf8Mojibake(value), "mojibake detected: " + Preview(value));
        // Round-trip UTF-8 bytes must not change the string.
        var bytes = Encoding.UTF8.GetBytes(value);
        Assert.Equal(value, Encoding.UTF8.GetString(bytes));
    }

    [Fact]
    public void AttentionCopy_MatchesExpectedHumanReadable()
    {
        Assert.Equal("Требует внимания", AttentionCopy.PanelTitle);
        Assert.Equal("Сервер не в сети", AttentionCopy.ServerOffline);
        Assert.Equal("Операция завершилась с ошибкой", AttentionCopy.OperationFailed);
        Assert.Equal("Неопределённый результат обновления", AttentionCopy.UnknownUpdateOutcome);
        Assert.Equal("Обновление сертификата", AttentionCopy.RenewCertificate);
        Assert.Equal("Нет данных", AttentionCopy.NoData);
        Assert.Contains('ё', AttentionCopy.CertExpired); // истёк
        Assert.Contains('ё', AttentionCopy.UnknownUpdateOutcome); // Неопределённый
    }

    [Fact]
    public void JsonSerialization_PreservesCyrillic()
    {
        var payload = new Dictionary<string, string>
        {
            ["title"] = AttentionCopy.OperationFailed,
            ["cmd"] = AttentionCopy.RenewCertificate,
            ["special"] = "ёЁ—→№"
        };
        var json = JsonSerializer.Serialize(payload);
        Assert.False(LooksLikeUtf8Mojibake(json));
        var back = JsonSerializer.Deserialize<Dictionary<string, string>>(json)!;
        Assert.Equal(AttentionCopy.OperationFailed, back["title"]);
        Assert.Equal(AttentionCopy.RenewCertificate, back["cmd"]);
        Assert.Equal("ёЁ—→№", back["special"]);
        // Explicit UTF-8 wire bytes round-trip
        var utf8 = Encoding.UTF8.GetBytes(json);
        var again = JsonSerializer.Deserialize<Dictionary<string, string>>(utf8)!;
        Assert.Equal(AttentionCopy.OperationFailed, again["title"]);
    }

    [Fact]
    public void UiText_CommandType_RenewCertificate_MatchesAttentionCopy()
    {
        Assert.Equal(AttentionCopy.RenewCertificate, UiText.CommandType(NodeCommandType.RenewCertificate));
        Assert.False(LooksLikeUtf8Mojibake(UiText.CommandType(NodeCommandType.RenewCertificate)));
    }

    [Fact]
    public void InfrastructureAssembly_EmbedsCorrectAttentionLiterals()
    {
        // Defect path: Release compile under CP1251 used to bake mojibake into this assembly.
        Encoding.RegisterProvider(CodePagesEncodingProvider.Instance);
        var asm = typeof(DashboardQueryService).Assembly;
        using var fs = File.OpenRead(asm.Location);
        using var ms = new MemoryStream();
        fs.CopyTo(ms);
        var bytes = ms.ToArray();

        Assert.True(ContainsUtf16Le(bytes, AttentionCopy.ServerOffline),
            "Release Infrastructure.dll missing correct UTF-16 'Сервер не в сети'");
        Assert.False(ContainsUtf16Le(bytes, MojibakeOfUtf8(AttentionCopy.ServerOffline)),
            "Release Infrastructure.dll still contains CP1251 mojibake of 'Сервер не в сети'");
    }

    [Theory]
    [InlineData(NodeCommandStatus.Failed, "rolled_back_healthy", false)]
    [InlineData(NodeCommandStatus.Expired, "expired", false)]
    [InlineData(NodeCommandStatus.Failed, "renew_failed", true)]
    [InlineData(NodeCommandStatus.Failed, "expired_outcome_unknown", true)]
    [InlineData(NodeCommandStatus.Expired, "outcome_unknown", true)]
    [InlineData(NodeCommandStatus.Failed, "rollback_failed", true)]
    [InlineData(NodeCommandStatus.Succeeded, "updated_healthy", false)]
    public void AttentionCommandPolicy_Actionable(NodeCommandStatus status, string code, bool expected)
    {
        var now = DateTime.UtcNow;
        Assert.Equal(expected,
            AttentionCommandPolicy.IsActionableAttentionCommand(status, code, now.AddHours(-3), now));
    }

    [Fact]
    public void AttentionCommandPolicy_OldNonUnknown_NotActionable()
    {
        var now = DateTime.UtcNow;
        Assert.False(AttentionCommandPolicy.IsActionableAttentionCommand(
            NodeCommandStatus.Failed, "renew_failed", now.AddDays(-10), now));
    }

    /// <summary>
    /// Classic UTF-8 bytes misread as Windows-1251 then re-encoded as UTF-8.
    /// </summary>
    public static bool LooksLikeUtf8Mojibake(string s)
    {
        if (string.IsNullOrEmpty(s)) return false;
        // High density of U+0410–U+042F followed by combining-looking pairs typical of Р… patterns
        // for mis-decoded Cyrillic: "Р" (U+0420) often precedes the rest.
        var mojibakeHits = 0;
        for (var i = 0; i < s.Length - 1; i++)
        {
            if (s[i] == '\u0420' && (s[i + 1] is '\u0451' or '\u0401' or '\u0435' or '\u0415'
                    or '\u00B5' or '\u040E' or '\u045E' or '\u0430' or '\u0410'))
                mojibakeHits++;
        }

        return mojibakeHits >= 2 || s.Contains("РЎРµСЂ", StringComparison.Ordinal)
               || s.Contains("РўСЂРµР±", StringComparison.Ordinal);
    }

    private static string MojibakeOfUtf8(string correct)
    {
        // Simulate the defect: UTF-8 bytes interpreted as CP1251 code units.
        var utf8 = Encoding.UTF8.GetBytes(correct);
        var cp1251 = Encoding.GetEncoding(1251);
        return cp1251.GetString(utf8);
    }

    private static bool ContainsUtf16Le(byte[] hay, string needle)
    {
        var n = Encoding.Unicode.GetBytes(needle);
        for (var i = 0; i <= hay.Length - n.Length; i++)
        {
            var ok = true;
            for (var j = 0; j < n.Length; j++)
            {
                if (hay[i + j] != n[j]) { ok = false; break; }
            }
            if (ok) return true;
        }

        return false;
    }

    private static string Preview(string s) => s.Length <= 64 ? s : s[..64];
}
