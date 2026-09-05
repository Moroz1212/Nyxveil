using System.Text.Json;
using Nyxveil.ControlPlane.Application.Serialization;
using Xunit;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class UtcDateTimeJsonConverterTests
{
    [Fact]
    public void Serializes_Unspecified_DateTime_As_Utc_With_Z()
    {
        // Production interop defect: STJ default omitted zone → Go time.Time failed.
        var dt = new DateTime(2026, 9, 5, 16, 3, 33, DateTimeKind.Unspecified)
            .AddTicks(2935527); // 7 .NET fractional digits

        var opts = new JsonSerializerOptions();
        opts.Converters.Add(new UtcDateTimeJsonConverter());
        var json = JsonSerializer.Serialize(dt, opts);

        Assert.Equal("\"2026-09-05T16:03:33.2935527Z\"", json);
    }

    [Fact]
    public void Serializes_DateTimeOffset_As_Z_Not_PlusZero()
    {
        var dto = new DateTimeOffset(2026, 9, 5, 16, 3, 33, 293, TimeSpan.Zero)
            .AddTicks(5527);

        var opts = new JsonSerializerOptions();
        opts.Converters.Add(new UtcDateTimeOffsetJsonConverter());
        var json = JsonSerializer.Serialize(dto, opts);

        Assert.EndsWith("Z\"", json);
        Assert.DoesNotContain("+00:00", json);
        Assert.Contains("2026-09-05T16:03:33.2935527Z", json);
    }
}
