using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Infrastructure.Security;

public static class CatalogCanonicalJson
{
    private static readonly JsonSerializerOptions CanonOptions = CreateCanonOptions();

    public static byte[] BuildCanonicalPayload(CatalogDto catalog)
    {
        var locations = catalog.Locations
            .OrderBy(l => l.LocationId, StringComparer.Ordinal)
            .ToList();
        var nodes = catalog.Nodes
            .OrderBy(n => n.NodeId, StringComparer.Ordinal)
            .ToList();

        // Match Go controlplane/catalog canonicalPayload: append(nil, empty...) stays nil → JSON null.
        var canon = new CatalogCanon
        {
            Version = catalog.Version,
            Locations = locations.Count == 0 ? null : locations,
            Nodes = nodes.Count == 0 ? null : nodes,
            IssuedAt = DateTime.SpecifyKind(catalog.IssuedAt.ToUniversalTime(), DateTimeKind.Utc),
            ExpiresAt = DateTime.SpecifyKind(catalog.ExpiresAt.ToUniversalTime(), DateTimeKind.Utc)
        };

        return JsonSerializer.SerializeToUtf8Bytes(canon, CanonOptions);
    }

    private static JsonSerializerOptions CreateCanonOptions()
    {
        var opts = new JsonSerializerOptions
        {
            PropertyNamingPolicy = null,
            DefaultIgnoreCondition = JsonIgnoreCondition.Never
        };
        opts.Converters.Add(new Rfc3339NanoDateTimeConverter());
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
/// Approximates Go encoding/json time.Time RFC3339Nano UTC (trims trailing fractional zeros).
/// </summary>
public sealed class Rfc3339NanoDateTimeConverter : JsonConverter<DateTime>
{
    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        return DateTime.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options)
    {
        var utc = DateTime.SpecifyKind(value.ToUniversalTime(), DateTimeKind.Utc);
        var formatted = utc.ToString("yyyy-MM-dd'T'HH:mm:ss.fffffff'Z'", CultureInfo.InvariantCulture);
        var dot = formatted.IndexOf('.');
        if (dot >= 0)
        {
            var z = formatted.Length - 1;
            while (z > dot && formatted[z - 1] == '0')
                z--;
            formatted = z == dot + 1
                ? formatted[..dot] + "Z"
                : formatted[..z] + "Z";
        }

        writer.WriteStringValue(formatted);
    }
}
