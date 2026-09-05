using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Application.Serialization;

/// <summary>
/// Serializes <see cref="DateTime"/> as unambiguous UTC RFC3339 with trailing Z
/// (fractional digits trimmed). Used for Node API and other external JSON DTOs.
/// </summary>
public sealed class UtcDateTimeJsonConverter : JsonConverter<DateTime>
{
    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        return DateTime.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options)
    {
        writer.WriteStringValue(FormatUtc(value));
    }

    internal static string FormatUtc(DateTime value)
    {
        var utc = value.Kind switch
        {
            DateTimeKind.Utc => value,
            DateTimeKind.Local => value.ToUniversalTime(),
            // Unspecified DB/UTC wall times are treated as UTC (no host-local conversion).
            _ => DateTime.SpecifyKind(value, DateTimeKind.Utc)
        };
        var formatted = utc.ToString("yyyy-MM-dd'T'HH:mm:ss.fffffff'Z'", CultureInfo.InvariantCulture);
        var dot = formatted.IndexOf('.');
        if (dot < 0)
            return formatted;
        var z = formatted.Length - 1;
        while (z > dot && formatted[z - 1] == '0')
            z--;
        return z == dot + 1
            ? formatted[..dot] + "Z"
            : formatted[..z] + "Z";
    }
}

public sealed class UtcNullableDateTimeJsonConverter : JsonConverter<DateTime?>
{
    public override DateTime? Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType == JsonTokenType.Null)
            return null;
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        return DateTime.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    public override void Write(Utf8JsonWriter writer, DateTime? value, JsonSerializerOptions options)
    {
        if (value is null)
        {
            writer.WriteNullValue();
            return;
        }
        writer.WriteStringValue(UtcDateTimeJsonConverter.FormatUtc(value.Value));
    }
}

/// <summary>
/// Serializes <see cref="DateTimeOffset"/> as UTC with trailing Z (not +00:00).
/// </summary>
public sealed class UtcDateTimeOffsetJsonConverter : JsonConverter<DateTimeOffset>
{
    public override DateTimeOffset Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        return DateTimeOffset.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    public override void Write(Utf8JsonWriter writer, DateTimeOffset value, JsonSerializerOptions options)
    {
        writer.WriteStringValue(UtcDateTimeJsonConverter.FormatUtc(value.UtcDateTime));
    }
}

public sealed class UtcNullableDateTimeOffsetJsonConverter : JsonConverter<DateTimeOffset?>
{
    public override DateTimeOffset? Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType == JsonTokenType.Null)
            return null;
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        return DateTimeOffset.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    public override void Write(Utf8JsonWriter writer, DateTimeOffset? value, JsonSerializerOptions options)
    {
        if (value is null)
        {
            writer.WriteNullValue();
            return;
        }
        writer.WriteStringValue(UtcDateTimeJsonConverter.FormatUtc(value.Value.UtcDateTime));
    }
}
