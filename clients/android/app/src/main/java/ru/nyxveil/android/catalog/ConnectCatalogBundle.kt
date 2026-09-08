package ru.nyxveil.android.catalog

import java.security.MessageDigest
import java.time.Instant

/**
 * Immutable catalog snapshot for Connect / Begin.
 * [rawSignedCatalog] is the exact Control Plane HTTP body — never re-serialized.
 */
data class ConnectCatalogBundle(
    val rawSignedCatalog: ByteArray,
    val keys: CatalogKeys,
    val signed: SignedCatalogDto,
    val source: String,
    val rawHash12: String,
    val issuedAt: Instant,
    val expiresAt: Instant,
    val verifiedAt: Instant,
    val remainingSeconds: Long,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other !is ConnectCatalogBundle) return false
        return rawSignedCatalog.contentEquals(other.rawSignedCatalog) &&
            keys == other.keys &&
            rawHash12 == other.rawHash12
    }

    override fun hashCode(): Int = rawHash12.hashCode()

    companion object {
        fun hash12(raw: ByteArray): String {
            val dig = MessageDigest.getInstance("SHA-256").digest(raw)
            return dig.take(6).joinToString("") { b -> "%02x".format(b) }
        }
    }
}
