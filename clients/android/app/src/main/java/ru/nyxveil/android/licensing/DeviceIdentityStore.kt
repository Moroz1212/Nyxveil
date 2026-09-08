package ru.nyxveil.android.licensing

import android.content.Context
import android.content.SharedPreferences
import android.os.Build
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.security.KeyPairGenerator
import java.security.SecureRandom
import java.util.Base64
import java.util.UUID

/**
 * Persistent device_id + Ed25519 keypair (encrypted). platform=android.
 */
class DeviceIdentityStore(context: Context) {
    private val prefs: SharedPreferences = createPrefs(context)

    data class Identity(
        val deviceId: String,
        val publicKeyRaw32: ByteArray,
        val privateKeyPkcs8: ByteArray,
        val platform: String = "android",
        val deviceName: String,
    ) {
        /** 32-byte Ed25519 seed for the Go bridge (not PKCS#8). */
        fun privateKeySeed32(): ByteArray = extractEd25519Seed(privateKeyPkcs8)
    }

    fun getOrCreate(): Identity {
        val existingId = prefs.getString(KEY_DEVICE_ID, null)
        val pubB64 = prefs.getString(KEY_PUBLIC, null)
        val privB64 = prefs.getString(KEY_PRIVATE, null)
        if (!existingId.isNullOrBlank() && !pubB64.isNullOrBlank() && !privB64.isNullOrBlank()) {
            return Identity(
                deviceId = existingId,
                publicKeyRaw32 = Base64.getDecoder().decode(pubB64),
                privateKeyPkcs8 = Base64.getDecoder().decode(privB64),
                deviceName = prefs.getString(KEY_NAME, defaultDeviceName()) ?: defaultDeviceName(),
            )
        }
        val kpg = KeyPairGenerator.getInstance("Ed25519")
        val pair = kpg.generateKeyPair()
        val publicRaw = extractRawEd25519Public(pair.public.encoded)
        val privatePkcs8 = pair.private.encoded
        val deviceId = UUID.randomUUID().toString()
        val name = defaultDeviceName()
        prefs.edit()
            .putString(KEY_DEVICE_ID, deviceId)
            .putString(KEY_PUBLIC, Base64.getEncoder().encodeToString(publicRaw))
            .putString(KEY_PRIVATE, Base64.getEncoder().encodeToString(privatePkcs8))
            .putString(KEY_NAME, name)
            .apply()
        return Identity(
            deviceId = deviceId,
            publicKeyRaw32 = publicRaw,
            privateKeyPkcs8 = privatePkcs8,
            deviceName = name,
        )
    }

    fun redactDeviceId(deviceId: String): String {
        if (deviceId.length <= 8) return "••••"
        return deviceId.take(4) + "…" + deviceId.takeLast(4)
    }

    companion object {
        private const val FILE = "nyxveil_device_identity"
        private const val KEY_DEVICE_ID = "device_id"
        private const val KEY_PUBLIC = "ed25519_public_raw_b64"
        private const val KEY_PRIVATE = "ed25519_private_pkcs8_b64"
        private const val KEY_NAME = "device_name"

        private fun createPrefs(context: Context): SharedPreferences {
            val masterKey = MasterKey.Builder(context)
                .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                .build()
            return EncryptedSharedPreferences.create(
                context,
                FILE,
                masterKey,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
            )
        }

        private fun defaultDeviceName(): String {
            val model = Build.MODEL?.takeIf { it.isNotBlank() } ?: "Android"
            return "Android · $model"
        }

        /** SubjectPublicKeyInfo → raw 32-byte Ed25519 public key. */
        private fun extractRawEd25519Public(x509: ByteArray): ByteArray {
            if (x509.size >= 32) {
                return x509.copyOfRange(x509.size - 32, x509.size)
            }
            throw IllegalStateException("Некорректный публичный ключ Ed25519")
        }

        /** PKCS#8 Ed25519 private key → 32-byte seed (RFC 8410). */
        fun extractEd25519Seed(pkcs8: ByteArray): ByteArray {
            // Look for OCTET STRING length 0x20 (32) holding the seed.
            for (i in 0 until pkcs8.size - 34) {
                if (pkcs8[i] == 0x04.toByte() && pkcs8[i + 1] == 0x20.toByte()) {
                    return pkcs8.copyOfRange(i + 2, i + 34)
                }
            }
            if (pkcs8.size >= 32) {
                return pkcs8.copyOfRange(pkcs8.size - 32, pkcs8.size)
            }
            throw IllegalStateException("Некорректный приватный ключ Ed25519")
        }

        @Suppress("unused")
        private fun randomFallbackId(): String {
            val bytes = ByteArray(16)
            SecureRandom().nextBytes(bytes)
            return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
        }
    }
}
