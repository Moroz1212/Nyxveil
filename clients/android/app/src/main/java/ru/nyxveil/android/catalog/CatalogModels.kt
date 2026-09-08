package ru.nyxveil.android.catalog

data class LocationDto(
    val locationId: String = "",
    val country: String = "",
    val countryCode: String = "",
    val city: String = "",
    val displayName: String = "",
    val enabled: Boolean = false,
)

data class EndpointDto(
    val host: String = "",
    val port: Int = 0,
    val profiles: List<String> = emptyList(),
    val ipFamily: String? = null,
)

data class HealthInfoDto(
    val healthy: Boolean = false,
    val latencyMs: Double = 0.0,
    val sessionCount: Int = 0,
    val cpuPercent: Double = 0.0,
    val memoryPercent: Double = 0.0,
)

data class NodeRegistryEntryDto(
    val nodeId: String = "",
    val locationId: String = "",
    val country: String = "",
    val city: String = "",
    val displayName: String = "",
    val status: String = "",
    val enabled: Boolean = false,
    val testOnly: Boolean = false,
    val draining: Boolean = false,
    val protocolVersion: Int = 0,
    val serverVersion: String = "",
    val endpoints: List<EndpointDto> = emptyList(),
    val serverName: String? = null,
    val spkiPin: ByteArray? = null,
    val capacity: Int = 0,
    val currentSessions: Int = 0,
    val health: HealthInfoDto = HealthInfoDto(),
    val lastSeen: String = "",
)

data class CatalogDto(
    val version: String = "",
    val locations: List<LocationDto> = emptyList(),
    val nodes: List<NodeRegistryEntryDto> = emptyList(),
    val issuedAt: String = "",
    val expiresAt: String = "",
)

data class SignedCatalogDto(
    val catalog: CatalogDto = CatalogDto(),
    val keyId: String = "",
    val signature: ByteArray = ByteArray(0),
)

data class CatalogKeys(
    val issuer: String = "",
    val keys: Map<String, String> = emptyMap(),
    val updatedAt: Long = 0L,
)

data class LocationUi(
    val locationId: String,
    val displayName: String,
    val city: String,
    val country: String,
)

data class CatalogVerifyReport(
    val keyId: String,
    val issuedAtEpochMs: Long,
    val expiresAtEpochMs: Long,
    val nowEpochMs: Long,
    val remainingSeconds: Long,
    val signature: String,
    val temporalValidation: String,
)

class CatalogTemporalException(
    message: String,
    val report: CatalogVerifyReport,
) : Exception(message)

class CatalogSignatureException(message: String) : Exception(message)
