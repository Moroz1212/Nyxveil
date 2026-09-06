using System.Security.Cryptography;
using System.Text.Json;
using NSec.Cryptography;

namespace Nyxveil.Client.Core;

/// <summary>
/// Stable device_id + Ed25519 keypair. Private seed is DPAPI-protected (CurrentUser).
/// Never stored under LocalSystem / never sent as License Credential.
/// </summary>
public static class DeviceIdentityStore
{
    private static readonly JsonSerializerOptions JsonOpts = new() { WriteIndented = true };

    private static string StorePath =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            "Nyxveil", "device.json");

    public sealed class DeviceIdentity
    {
        public string DeviceId { get; init; } = "";
        /// <summary>32-byte Ed25519 public key.</summary>
        public byte[] PublicKey { get; init; } = Array.Empty<byte>();
        /// <summary>64-byte Go crypto/ed25519 private key (seed ‖ public).</summary>
        public byte[] PrivateKeyGoFormat { get; init; } = Array.Empty<byte>();
    }

    public static DeviceIdentity LoadOrCreate()
    {
        var existing = TryLoad();
        if (existing is not null)
            return existing;
        return CreateNew();
    }

    public static DeviceIdentity? TryLoad()
    {
        try
        {
            if (!File.Exists(StorePath))
                return null;
            var dto = JsonSerializer.Deserialize<DeviceFile>(File.ReadAllText(StorePath));
            if (dto is null || string.IsNullOrWhiteSpace(dto.DeviceId) ||
                string.IsNullOrWhiteSpace(dto.ProtectedSeedBase64) ||
                string.IsNullOrWhiteSpace(dto.PublicKeyBase64))
                return null;

            var protectedSeed = Convert.FromBase64String(dto.ProtectedSeedBase64);
            var seed = ProtectedData.Unprotect(protectedSeed, optionalEntropy: null, DataProtectionScope.CurrentUser);
            if (seed.Length != 32)
                return null;
            var pub = Convert.FromBase64String(dto.PublicKeyBase64);
            if (pub.Length != 32)
                return null;

            return new DeviceIdentity
            {
                DeviceId = dto.DeviceId,
                PublicKey = pub,
                PrivateKeyGoFormat = ExpandGoPrivateKey(seed, pub)
            };
        }
        catch
        {
            return null;
        }
    }

    public static DeviceIdentity CreateNew()
    {
        var algo = SignatureAlgorithm.Ed25519;
        using var key = Key.Create(algo, new KeyCreationParameters
        {
            ExportPolicy = KeyExportPolicies.AllowPlaintextExport
        });
        var seed = key.Export(KeyBlobFormat.RawPrivateKey);
        var pub = key.PublicKey.Export(KeyBlobFormat.RawPublicKey);
        try
        {
            var deviceId = "win-" + Guid.NewGuid().ToString("N");
            var protectedSeed = ProtectedData.Protect(seed, optionalEntropy: null, DataProtectionScope.CurrentUser);
            var dto = new DeviceFile
            {
                DeviceId = deviceId,
                PublicKeyBase64 = Convert.ToBase64String(pub),
                ProtectedSeedBase64 = Convert.ToBase64String(protectedSeed)
            };
            var dir = Path.GetDirectoryName(StorePath)!;
            Directory.CreateDirectory(dir);
            File.WriteAllText(StorePath, JsonSerializer.Serialize(dto, JsonOpts));

            return new DeviceIdentity
            {
                DeviceId = deviceId,
                PublicKey = pub,
                PrivateKeyGoFormat = ExpandGoPrivateKey(seed, pub)
            };
        }
        finally
        {
            CryptographicOperations.ZeroMemory(seed);
        }
    }

    private static byte[] ExpandGoPrivateKey(byte[] seed, byte[] publicKey)
    {
        if (seed.Length != 32 || publicKey.Length != 32)
            throw new InvalidOperationException("Ed25519 seed/public must be 32 bytes");
        var go = new byte[64];
        Buffer.BlockCopy(seed, 0, go, 0, 32);
        Buffer.BlockCopy(publicKey, 0, go, 32, 32);
        return go;
    }

    private sealed class DeviceFile
    {
        public string DeviceId { get; set; } = "";
        public string PublicKeyBase64 { get; set; } = "";
        public string ProtectedSeedBase64 { get; set; } = "";
    }
}
