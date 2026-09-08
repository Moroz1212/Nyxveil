package ru.nyxveil.android.diagnostics

data class DiagSnapshot(
    val licenseStatus: String = "—",
    val catalogStatus: String = "—",
    val vpnPermissionStatus: String = "—",
    val transportStatus: String = "—",
    val protocolStatus: String = "NVP/1",
    val tunnelStatus: String = "—",
    val dnsStatus: String = "—",
    val mtuStatus: String = "—",
    val connectionState: String = "—",
    val deviceIdRedacted: String = "—",
    val bridgeStatus: String = "—",
    /** Sanitized catalog engineering fields (no secrets / no CP URL). */
    val catalogSource: String = "—",
    val catalogKeyId: String = "—",
    val catalogSignatureStatus: String = "—",
    val catalogTemporalStatus: String = "—",
    val catalogHttpStage: String = "—",
    val tunReadPackets: String = "—",
    val nvpTxPackets: String = "—",
    val nvpRxPackets: String = "—",
    val tunWritePackets: String = "—",
    val trafficStatus: String = "—",
    val txPumpAlive: String = "—",
)
