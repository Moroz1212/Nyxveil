using System.Globalization;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Serialization;
using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Application.Serialization;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

/// <summary>
/// Builds the exact UTF-8 bytes that Frozen Core <c>catalog.canonicalPayload</c> produces
/// via Go <c>encoding/json.Marshal</c> (HTML escapes, float64 'g', RFC3339Nano, omitempty).
/// </summary>
public static class CatalogCanonicalJson
{
    private static readonly JsonSerializerOptions CanonOptions = CreateCanonOptions();

    public static byte[] BuildCanonicalPayload(CatalogDto catalog)
    {
        var locations = catalog.Locations
            .OrderBy(l => l.LocationId, StringComparer.Ordinal)
            .Select(NormalizeLocation)
            .ToList();
        var nodes = catalog.Nodes
            .OrderBy(n => n.NodeId, StringComparer.Ordinal)
            .Select(NormalizeNode)
            .ToList();

        // Match Go controlplane/catalog canonicalPayload: append(nil, empty...) stays nil → JSON null.
        var canon = new CatalogCanon
        {
            Version = catalog.Version ?? string.Empty,
            Locations = locations.Count == 0 ? null : locations,
            Nodes = nodes.Count == 0 ? null : nodes,
            IssuedAt = catalog.IssuedAt,
            ExpiresAt = catalog.ExpiresAt
        };

        return JsonSerializer.SerializeToUtf8Bytes(canon, CanonOptions);
    }

    private static LocationDto NormalizeLocation(LocationDto l) => new()
    {
        LocationId = l.LocationId ?? string.Empty,
        Country = l.Country ?? string.Empty,
        CountryCode = l.CountryCode ?? string.Empty,
        City = l.City ?? string.Empty,
        DisplayName = l.DisplayName ?? string.Empty,
        Enabled = l.Enabled
    };

    private static NodeRegistryEntryDto NormalizeNode(NodeRegistryEntryDto n) => new()
    {
        NodeId = n.NodeId ?? string.Empty,
        LocationId = n.LocationId ?? string.Empty,
        Country = n.Country ?? string.Empty,
        City = n.City ?? string.Empty,
        DisplayName = n.DisplayName ?? string.Empty,
        Status = n.Status ?? string.Empty,
        Enabled = n.Enabled,
        TestOnly = n.TestOnly,
        Draining = n.Draining,
        ProtocolVersion = n.ProtocolVersion,
        ServerVersion = n.ServerVersion ?? string.Empty,
        Endpoints = (n.Endpoints ?? Array.Empty<EndpointDto>()).Select(NormalizeEndpoint).ToList(),
        // Go omitempty: empty string / empty slice are omitted (same as null).
        ServerName = string.IsNullOrEmpty(n.ServerName) ? null : n.ServerName,
        SpkiPin = n.SpkiPin is { Length: > 0 } ? n.SpkiPin : null,
        Capacity = n.Capacity,
        CurrentSessions = n.CurrentSessions,
        Health = n.Health ?? new HealthInfoDto(),
        LastSeen = n.LastSeen
    };

    private static EndpointDto NormalizeEndpoint(EndpointDto e) => new()
    {
        Host = e.Host ?? string.Empty,
        Port = e.Port,
        // Preserve null vs empty: Go marshals nil slice as null, empty slice as [].
        Profiles = e.Profiles,
        IpFamily = string.IsNullOrEmpty(e.IpFamily) ? null : e.IpFamily
    };

    private static JsonSerializerOptions CreateCanonOptions()
    {
        var opts = new JsonSerializerOptions
        {
            PropertyNamingPolicy = null,
            DefaultIgnoreCondition = JsonIgnoreCondition.Never,
            // Non-ASCII must stay as UTF-8 bytes (Go encoding/json default). HTML + controls
            // are escaped by GoJsonStringConverter with lowercase \uXXXX (Go htmlEscape).
            Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping
        };
        opts.Converters.Add(new GoCompatibleDateTimeConverter());
        opts.Converters.Add(new GoFloat64Converter());
        opts.Converters.Add(new GoJsonStringConverter());
        return opts;
    }

    private sealed class CatalogCanon
    {
        [JsonPropertyName("version")]
        public string Version { get; set; } = string.Empty;

        [JsonPropertyName("locations")]
        public List<LocationDto>? Locations { get; set; }

        [JsonPropertyName("nodes")]
        public List<NodeRegistryEntryDto>? Nodes { get; set; }

        [JsonPropertyName("issued_at")]
        public DateTime IssuedAt { get; set; }

        [JsonPropertyName("expires_at")]
        public DateTime ExpiresAt { get; set; }
    }
}

/// <summary>
/// Matches Go encoding/json time.Time RFC3339Nano UTC. Unspecified kind = UTC wall
/// (same as <see cref="UtcDateTimeJsonConverter"/>) — never host-local conversion.
/// </summary>
public sealed class GoCompatibleDateTimeConverter : JsonConverter<DateTime>
{
    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        return DateTime.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options)
    {
        writer.WriteStringValue(UtcDateTimeJsonConverter.FormatUtc(value));
    }
}

/// <summary>Kept for call sites / tests that reference the historical name.</summary>
public sealed class Rfc3339NanoDateTimeConverter : JsonConverter<DateTime>
{
    private static readonly GoCompatibleDateTimeConverter Inner = new();

    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
        => Inner.Read(ref reader, typeToConvert, options);

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options)
        => Inner.Write(writer, value, options);
}

/// <summary>
/// Matches Go encoding/json float64 encoding: strconv.AppendFloat(f, 'g', -1, 64).
/// Lowercase exponent marker; negative zero collapses to 0.
/// </summary>
public sealed class GoFloat64Converter : JsonConverter<double>
{
    public override double Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
        => reader.GetDouble();

    public override void Write(Utf8JsonWriter writer, double value, JsonSerializerOptions options)
    {
        if (double.IsNaN(value) || double.IsInfinity(value))
            throw new InvalidOperationException("catalog floats must be finite");
        writer.WriteRawValue(GoJsonNumber.FormatFloat64(value));
    }
}

/// <summary>
/// Escapes strings like Go encoding/json with EscapeHTML=true: &lt; &gt; &amp;,
/// controls, U+2028/U+2029 → lowercase \uXXXX; other Unicode left as UTF-8.
/// </summary>
public sealed class GoJsonStringConverter : JsonConverter<string>
{
    public override string Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
        => reader.GetString() ?? string.Empty;

    public override void Write(Utf8JsonWriter writer, string value, JsonSerializerOptions options)
    {
        writer.WriteRawValue(GoJsonString.Quote(value ?? string.Empty));
    }
}

public static class GoJsonNumber
{
    public static string FormatFloat64(double value)
    {
        // encoding/json uses strconv.AppendFloat(buf, f, 'g', -1, bitSize).
        // Preserve negative zero as "-0" (Go json.Marshal does the same).
        if (value == 0)
            return double.IsNegative(value) ? "-0" : "0";

        // "R" is round-trip; then normalize exponent marker/sign to Go's style.
        var s = value.ToString("R", CultureInfo.InvariantCulture);
        s = s.Replace('E', 'e');
        // Go always emits a sign in the exponent: e+21 / e-10 (never e21).
        var e = s.IndexOf('e');
        if (e >= 0 && e + 1 < s.Length && s[e + 1] is not ('+' or '-'))
            s = s.Insert(e + 1, "+");
        return s;
    }
}

internal static class GoJsonString
{
    private static readonly char[] Hex = "0123456789abcdef".ToCharArray();

    public static string Quote(string value)
    {
        var sb = new StringBuilder(value.Length + 2);
        sb.Append('"');
        foreach (var ch in value)
        {
            switch (ch)
            {
                case '"':
                case '\\':
                    sb.Append('\\').Append(ch);
                    break;
                case '\b':
                    sb.Append("\\b");
                    break;
                case '\f':
                    sb.Append("\\f");
                    break;
                case '\n':
                    sb.Append("\\n");
                    break;
                case '\r':
                    sb.Append("\\r");
                    break;
                case '\t':
                    sb.Append("\\t");
                    break;
                case '<':
                    sb.Append("\\u003c");
                    break;
                case '>':
                    sb.Append("\\u003e");
                    break;
                case '&':
                    sb.Append("\\u0026");
                    break;
                default:
                    if (ch < 0x20)
                    {
                        AppendUnicodeEscape(sb, ch);
                    }
                    else if (ch is '\u2028' or '\u2029')
                    {
                        AppendUnicodeEscape(sb, ch);
                    }
                    else
                    {
                        sb.Append(ch);
                    }
                    break;
            }
        }
        sb.Append('"');
        return sb.ToString();
    }

    private static void AppendUnicodeEscape(StringBuilder sb, char ch)
    {
        sb.Append("\\u");
        sb.Append(Hex[(ch >> 12) & 0xF]);
        sb.Append(Hex[(ch >> 8) & 0xF]);
        sb.Append(Hex[(ch >> 4) & 0xF]);
        sb.Append(Hex[ch & 0xF]);
    }
}
