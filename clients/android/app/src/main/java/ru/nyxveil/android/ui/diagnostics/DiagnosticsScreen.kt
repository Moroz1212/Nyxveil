package ru.nyxveil.android.ui.diagnostics

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import ru.nyxveil.android.diagnostics.DiagSnapshot
import ru.nyxveil.android.ui.theme.NyxBackgroundBrush
import ru.nyxveil.android.ui.theme.NyxCyan
import ru.nyxveil.android.ui.theme.NyxGlass
import ru.nyxveil.android.ui.theme.NyxMuted
import ru.nyxveil.android.ui.theme.NyxOnDark

@Composable
fun DiagnosticsScreen(snapshot: DiagSnapshot) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(NyxBackgroundBrush)
            .verticalScroll(rememberScrollState())
            .padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Text("Диагностика", style = MaterialTheme.typography.headlineMedium, color = NyxOnDark)
        Text(
            text = "Секреты скрыты. Адрес плоскости управления не отображается.",
            style = MaterialTheme.typography.bodyMedium,
            color = NyxMuted,
        )
        Section("ACCOUNT") {
            DiagRow("Лицензия", snapshot.licenseStatus)
            DiagRow("Устройство", snapshot.deviceIdRedacted)
        }
        Section("NETWORK") {
            DiagRow("Каталог", snapshot.catalogStatus)
            DiagRow("Ключ каталога", snapshot.catalogKeyId)
            DiagRow("Подпись каталога", snapshot.catalogSignatureStatus)
            DiagRow("Срок каталога", snapshot.catalogTemporalStatus)
        }
        Section("VPN") {
            DiagRow("Разрешение VPN", snapshot.vpnPermissionStatus)
            DiagRow("Туннель", snapshot.tunnelStatus)
            DiagRow("Состояние", snapshot.connectionState)
            DiagRow("DNS", snapshot.dnsStatus)
            DiagRow("MTU", snapshot.mtuStatus)
        }
        Section("DATAPLANE") {
            DiagRow("TUN read", snapshot.tunReadPackets)
            DiagRow("NVP TX", snapshot.nvpTxPackets)
            DiagRow("NVP RX", snapshot.nvpRxPackets)
            DiagRow("TUN write", snapshot.tunWritePackets)
            DiagRow("Traffic", snapshot.trafficStatus)
            DiagRow("TX pump", snapshot.txPumpAlive)
        }
        Section("ENGINE") {
            DiagRow("Протокол", snapshot.protocolStatus)
            DiagRow("Транспорт", snapshot.transportStatus)
            DiagRow("Мост", snapshot.bridgeStatus)
        }
    }
}

@Composable
private fun Section(title: String, content: @Composable () -> Unit) {
    Column(
        Modifier
            .fillMaxWidth()
            .background(NyxGlass, RoundedCornerShape(18.dp))
            .border(1.dp, NyxCyan.copy(alpha = 0.15f), RoundedCornerShape(18.dp))
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(title, style = MaterialTheme.typography.labelLarge, color = NyxCyan)
        content()
    }
}

@Composable
private fun DiagRow(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.labelLarge, color = NyxMuted)
        Text(value.ifBlank { "—" }, style = MaterialTheme.typography.bodyLarge, color = NyxOnDark)
    }
}
