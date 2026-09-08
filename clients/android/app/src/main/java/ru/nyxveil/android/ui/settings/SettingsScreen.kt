package ru.nyxveil.android.ui.settings

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import ru.nyxveil.android.ui.theme.NyxBackgroundBrush
import ru.nyxveil.android.ui.theme.NyxCyan
import ru.nyxveil.android.ui.theme.NyxGlass
import ru.nyxveil.android.ui.theme.NyxMuted
import ru.nyxveil.android.ui.theme.NyxOnDark

@Composable
fun SettingsScreen(
    autoConnect: Boolean,
    notificationsEnabled: Boolean,
    appVersion: String,
    onAutoConnectChange: (Boolean) -> Unit,
    onNotificationsChange: (Boolean) -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(NyxBackgroundBrush)
            .padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("Настройки", style = MaterialTheme.typography.headlineMedium, color = NyxOnDark)
        GlassBlock {
            SettingToggle(
                title = "Автоподключение",
                subtitle = "Подключаться при запуске, если настроено",
                checked = autoConnect,
                onCheckedChange = onAutoConnectChange,
            )
        }
        GlassBlock {
            SettingToggle(
                title = "Уведомления",
                subtitle = "Статус VPN в панели уведомлений",
                checked = notificationsEnabled,
                onCheckedChange = onNotificationsChange,
            )
        }
        GlassBlock {
            Text("О приложении", style = MaterialTheme.typography.titleMedium, color = NyxOnDark)
            Text("Nyxveil для Android", style = MaterialTheme.typography.bodyLarge, color = NyxOnDark)
            Text("Версия $appVersion", style = MaterialTheme.typography.bodyMedium, color = NyxMuted)
            Text("Протокол Nyxveil NVP/1", style = MaterialTheme.typography.bodyMedium, color = NyxMuted)
        }
    }
}

@Composable
private fun GlassBlock(content: @Composable () -> Unit) {
    Column(
        Modifier
            .fillMaxWidth()
            .background(NyxGlass, RoundedCornerShape(18.dp))
            .border(1.dp, NyxCyan.copy(alpha = 0.15f), RoundedCornerShape(18.dp))
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
        content = { content() },
    )
}

@Composable
private fun SettingToggle(
    title: String,
    subtitle: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
) {
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f).padding(end = 12.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium, color = NyxOnDark)
            Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = NyxMuted)
        }
        Switch(checked = checked, onCheckedChange = onCheckedChange)
    }
}
