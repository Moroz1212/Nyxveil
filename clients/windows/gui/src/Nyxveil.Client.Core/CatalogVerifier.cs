using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;
using NSec.Cryptography;

namespace Nyxveil.Client.Core;

/// <summary>
/// Catalog Ed25519 verify matching Control Plane CatalogCanonicalJson / Go controlplane/catalog.
/// </summary>
public static class CatalogVerifier
{
    public static SignedCatalogDto Parse(byte[] signedCatalogJson) =>
        JsonSerializer.Deserialize<SignedCatalogDto>(signedCatalogJson)
        ?? throw new InvalidOperationException("Пустой ответ каталога.");

    public static CatalogVerifyReport Verify(SignedCatalogDto signed,
        IReadOnlyDictionary<string, string> catalogKeysKidToStdBase64,
        DateTimeOffset? nowUtc = null)
    {
        if (string.IsNullOrWhiteSpace(signed.KeyId))
            throw new InvalidOperationException("В каталоге отсутствует key_id.");
        if (signed.Signature is not { Length: > 0 })
            throw new InvalidOperationException("В каталоге отсутствует подпись.");
        if (!catalogKeysKidToStdBase64.TryGetValue(signed.KeyId, out var b64) || string.IsNullOrWhiteSpace(b64))
            throw new InvalidOperationException($"Неизвестный ключ подписи каталога: {signed.KeyId}");

        byte[] pub;
        try
        {
            pub = Convert.FromBase64String(b64);
        }
        catch (FormatException ex)
        {
            throw new InvalidOperationException("Некорректный Base64 ключа каталога.", ex);
        }

        if (pub.Length != 32)
            throw new InvalidOperationException("Ключ каталога должен быть 32 байта Ed25519.");

        var payload = CatalogCanonicalJson.BuildCanonicalPayload(signed.Catalog);
        var algo = SignatureAlgorithm.Ed25519;
        var publicKey = PublicKey.Import(algo, pub, KeyBlobFormat.RawPublicKey);
        var signatureOk = algo.Verify(publicKey, payload, signed.Signature);
        if (!signatureOk)
            throw new InvalidOperationException("Подпись каталога недействительна.");

        var now = nowUtc ?? DateTimeOffset.UtcNow;
        var issued = CatalogTime.AsUtcInstant(signed.Catalog.IssuedAt);
        var expires = CatalogTime.AsUtcInstant(signed.Catalog.ExpiresAt);
        var temporalOk = CatalogTime.IsTemporallyValid(now, issued, expires);
        var remaining = (long)Math.Floor((expires - now).TotalSeconds);

        var report = new CatalogVerifyReport
        {
            KeyId = signed.KeyId,
            IssuedAt = issued,
            ExpiresAt = expires,
            NowUtc = now,
            RemainingSeconds = remaining,
            Signature = "PASS",
            TemporalValidation = temporalOk ? "PASS" : "FAIL"
        };

        if (!temporalOk)
        {
            throw new CatalogTemporalException(
                "Срок действия каталога истёк или ещё не начался.",
                report);
        }

        return report;
    }
}

public sealed class CatalogVerifyReport
{
    public string KeyId { get; init; } = "";
    public DateTimeOffset IssuedAt { get; init; }
    public DateTimeOffset ExpiresAt { get; init; }
    public DateTimeOffset NowUtc { get; init; }
    public long RemainingSeconds { get; init; }
    public string Signature { get; init; } = "";
    public string TemporalValidation { get; init; } = "";
}

public sealed class CatalogTemporalException : InvalidOperationException
{
    public CatalogVerifyReport Report { get; }

    public CatalogTemporalException(string message, CatalogVerifyReport report)
        : base(message)
    {
        Report = report;
    }
}

/// <summary>Canonical catalog JSON matching Go controlplane/catalog.canonicalPayload.</summary>
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

        var canon = new CatalogCanon
        {
            Version = catalog.Version,
            Locations = locations.Count == 0 ? null : locations,
            Nodes = nodes.Count == 0 ? null : nodes,
            IssuedAt = CatalogTime.NormalizeUtcWall(catalog.IssuedAt),
            ExpiresAt = CatalogTime.NormalizeUtcWall(catalog.ExpiresAt)
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
        public string Version { get; set; } = "";

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

/// <summary>Approximates Go encoding/json time.Time RFC3339Nano UTC.</summary>
public sealed class Rfc3339NanoDateTimeConverter : JsonConverter<DateTime>
{
    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        var s = reader.GetString() ?? throw new JsonException("expected date string");
        var parsed = DateTime.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);
        return CatalogTime.NormalizeUtcWall(parsed);
    }

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options)
    {
        var utc = CatalogTime.NormalizeUtcWall(value);
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
