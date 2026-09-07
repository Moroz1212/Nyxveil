using System.Text.Json;
using System.Text.Json.Serialization;

namespace Nyxveil.Client.Core;

/// <summary>
/// Persistent signed-catalog cache under LocalAppData (survives reinstall/upgrade).
/// Expired / not-yet-valid entries must never be used without a successful refresh.
/// </summary>
public static class CatalogCacheStore
{
    private static readonly JsonSerializerOptions JsonOpts = new()
    {
        WriteIndented = false
    };

    public static string CachePath =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Nyxveil", "catalog-cache.json");

    public static CatalogCacheEntry? TryLoad()
    {
        try
        {
            var path = CachePath;
            if (!File.Exists(path))
                return null;
            var json = File.ReadAllText(path);
            return JsonSerializer.Deserialize<CatalogCacheEntry>(json, JsonOpts);
        }
        catch
        {
            return null;
        }
    }

    public static void Save(byte[] signedCatalogJson, CatalogKeysResponse keys)
    {
        var dir = Path.GetDirectoryName(CachePath)!;
        Directory.CreateDirectory(dir);
        var entry = new CatalogCacheEntry
        {
            SavedAtUtc = DateTimeOffset.UtcNow,
            SignedCatalogJsonBase64 = Convert.ToBase64String(signedCatalogJson),
            Keys = new Dictionary<string, string>(keys.Keys),
            Issuer = keys.Issuer,
            KeysUpdatedAt = keys.UpdatedAt
        };
        var tmp = CachePath + ".tmp";
        File.WriteAllText(tmp, JsonSerializer.Serialize(entry, JsonOpts));
        File.Copy(tmp, CachePath, overwrite: true);
        File.Delete(tmp);
    }

    public static void Delete()
    {
        try
        {
            if (File.Exists(CachePath))
                File.Delete(CachePath);
        }
        catch
        {
            /* ignore */
        }
    }
}

public sealed class CatalogCacheEntry
{
    [JsonPropertyName("saved_at_utc")]
    public DateTimeOffset SavedAtUtc { get; set; }

    [JsonPropertyName("signed_catalog_json_b64")]
    public string SignedCatalogJsonBase64 { get; set; } = "";

    [JsonPropertyName("keys")]
    public Dictionary<string, string> Keys { get; set; } = new();

    [JsonPropertyName("issuer")]
    public string Issuer { get; set; } = "";

    [JsonPropertyName("keys_updated_at")]
    public long KeysUpdatedAt { get; set; }

    public byte[]? TryGetRaw()
    {
        try
        {
            if (string.IsNullOrWhiteSpace(SignedCatalogJsonBase64))
                return null;
            return Convert.FromBase64String(SignedCatalogJsonBase64);
        }
        catch
        {
            return null;
        }
    }

    public CatalogKeysResponse ToKeysResponse() => new()
    {
        Issuer = Issuer,
        Keys = Keys,
        UpdatedAt = KeysUpdatedAt
    };
}
