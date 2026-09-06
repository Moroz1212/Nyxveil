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

    public static void Verify(SignedCatalogDto signed, IReadOnlyDictionary<string, string> catalogKeysKidToStdBase64)
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
        if (!algo.Verify(publicKey, payload, signed.Signature))
            throw new InvalidOperationException("Подпись каталога недействительна.");

        var now = DateTime.UtcNow;
        var issued = DateTime.SpecifyKind(signed.Catalog.IssuedAt.ToUniversalTime(), DateTimeKind.Utc);
        var expires = DateTime.SpecifyKind(signed.Catalog.ExpiresAt.ToUniversalTime(), DateTimeKind.Utc);
        if (now > expires || now < issued)
            throw new InvalidOperationException("Срок действия каталога истёк или ещё не начался.");
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
