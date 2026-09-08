package ru.nyxveil.android.vpn

/**
 * Immutable connect payload: one catalog raw snapshot + ticket + identity.
 * VpnService must not reload catalog from cache/storage.
 */
data class PreparedConnectRequest(
    val locationId: String,
    val locationLabel: String,
    val accessTicket: String,
    val rawSignedCatalog: ByteArray,
    val catalogKeys: Map<String, String>,
    val devicePrivateKeySeed32: ByteArray,
    val catalogRawHash12: String,
    val catalogSource: String,
) {
    override fun equals(other: Any?): Boolean {
        if (this === other) return true
        if (other !is PreparedConnectRequest) return false
        return locationId == other.locationId &&
            catalogRawHash12 == other.catalogRawHash12 &&
            accessTicket == other.accessTicket
    }

    override fun hashCode(): Int = catalogRawHash12.hashCode() * 31 + locationId.hashCode()
}
