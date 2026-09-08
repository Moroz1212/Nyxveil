package ru.nyxveil.android.ui.home

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.remember
import androidx.compose.ui.geometry.Size
import ru.nyxveil.android.R
import ru.nyxveil.android.ui.AppUiState
import ru.nyxveil.android.ui.theme.NyxBackgroundBrush
import ru.nyxveil.android.ui.theme.NyxCyan
import ru.nyxveil.android.ui.theme.NyxGlass
import ru.nyxveil.android.ui.theme.NyxMuted
import ru.nyxveil.android.ui.theme.NyxOnDark
import ru.nyxveil.android.ui.theme.NyxSuccess
import ru.nyxveil.android.ui.theme.NyxTeal
import ru.nyxveil.android.vpn.ConnectionState

@Composable
fun HomeScreen(
    state: AppUiState,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
) {
    val connected = state.connectionState == ConnectionState.Connected
    val busy = state.connectionState.isBusy()
    val scroll = rememberScrollState()
    val context = LocalContext.current
    val heroId = remember {
        context.resources.getIdentifier("nyxveil_hero", "drawable", context.packageName)
    }

    Box(Modifier.fillMaxSize().background(NyxBackgroundBrush)) {
        if (heroId != 0) {
            Image(
                painter = painterResource(heroId),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
                alpha = 0.35f,
            )
        }
        Box(
            Modifier
                .fillMaxSize()
                .background(
                    Brush.verticalGradient(
                        listOf(Color(0xCC050814), Color(0x99070B14), Color(0xEE070B14)),
                    ),
                ),
        )
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(scroll)
                .padding(horizontal = 20.dp, vertical = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("Nyxveil", style = MaterialTheme.typography.displaySmall, color = NyxOnDark)
            Text(
                text = stringResource(R.string.tagline).uppercase(),
                style = MaterialTheme.typography.labelLarge,
                color = NyxCyan,
            )
            Spacer(Modifier.height(28.dp))
            ConnectionRing(state.connectionState)
            Spacer(Modifier.height(16.dp))
            Text(
                text = stateStatusLabel(state.connectionState),
                style = MaterialTheme.typography.titleLarge,
                color = if (connected) NyxSuccess else NyxOnDark,
            )
            if (connected && state.sessionStartedAtMs > 0) {
                Text(
                    text = formatSession(state.sessionStartedAtMs),
                    style = MaterialTheme.typography.bodyMedium,
                    color = NyxMuted,
                )
            }
            if (!state.statusMessage.isNullOrBlank() &&
                state.connectionState != ConnectionState.Connected
            ) {
                Spacer(Modifier.height(8.dp))
                Text(
                    text = state.statusMessage,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                    textAlign = TextAlign.Center,
                )
            }
            Spacer(Modifier.height(24.dp))
            GlassCard {
                Text("ЛОКАЦИЯ", style = MaterialTheme.typography.labelLarge)
                Text(
                    text = state.selectedLocationLabel.ifBlank { "Не выбрана" },
                    style = MaterialTheme.typography.headlineMedium,
                )
                Text(
                    text = listOf(state.selectedLocationCity, state.selectedLocationCountry)
                        .filter { it.isNotBlank() }
                        .joinToString(", ")
                        .ifBlank { "—" },
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            Spacer(Modifier.height(12.dp))
            GlassCard {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Metric("Сессия", if (connected) formatSession(state.sessionStartedAtMs) else "—")
                    Metric("Ping", state.pingMs?.let { "$it мс" } ?: "—")
                }
                Spacer(Modifier.height(10.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Metric("↓", if (connected) formatMbps(state.downloadMbps) else "—")
                    Metric("↑", if (connected) formatMbps(state.uploadMbps) else "—")
                }
                Spacer(Modifier.height(10.dp))
                Text("VPN IP: ${state.vpnIp.ifBlank { "—" }}", style = MaterialTheme.typography.bodyMedium)
                Text(
                    text = "Протокол: Nyxveil NVP/1",
                    style = MaterialTheme.typography.bodyMedium,
                )
                if (state.transport.isNotBlank()) {
                    Text(
                        text = "Транспорт: ${formatTransport(state.transport)}",
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
                if (state.mtu > 0) {
                    Text("MTU: ${state.mtu}", style = MaterialTheme.typography.bodyMedium)
                }
            }
            Spacer(Modifier.height(28.dp))
            if (connected || busy && state.connectionState != ConnectionState.Disconnected) {
                OutlinedButton(
                    onClick = onDisconnect,
                    enabled = state.connectionState != ConnectionState.Disconnecting,
                    modifier = Modifier.fillMaxWidth().height(52.dp),
                    shape = RoundedCornerShape(16.dp),
                    colors = ButtonDefaults.outlinedButtonColors(contentColor = NyxOnDark),
                ) {
                    Text(if (busy && !connected) "Отмена" else "Отключить")
                }
                Spacer(Modifier.height(10.dp))
            }
            if (!connected) {
                Button(
                    onClick = onConnect,
                    enabled = !busy && state.selectedLocationId.isNotBlank(),
                    modifier = Modifier.fillMaxWidth().height(56.dp),
                    shape = RoundedCornerShape(18.dp),
                    colors = ButtonDefaults.buttonColors(
                        containerColor = NyxTeal,
                        contentColor = Color(0xFF042F2E),
                        disabledContainerColor = NyxTeal.copy(alpha = 0.35f),
                    ),
                ) {
                    Text(
                        text = when {
                            busy -> stateStatusLabel(state.connectionState)
                            else -> "Подключить"
                        },
                        style = MaterialTheme.typography.titleMedium,
                    )
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun ConnectionRing(state: ConnectionState) {
    val connected = state == ConnectionState.Connected
    val busy = state.isBusy()
    val transition = rememberInfiniteTransition(label = "ring")
    val sweep by transition.animateFloat(
        initialValue = 0f,
        targetValue = 360f,
        animationSpec = infiniteRepeatable(tween(1800, easing = LinearEasing), RepeatMode.Restart),
        label = "sweep",
    )
    val pulse by transition.animateFloat(
        initialValue = 0.85f,
        targetValue = 1.1f,
        animationSpec = infiniteRepeatable(tween(1200), RepeatMode.Reverse),
        label = "pulse",
    )
    Canvas(modifier = Modifier.size(200.dp)) {
        val stroke = 10.dp.toPx()
        val radius = size.minDimension / 2f - stroke
        val center = Offset(size.width / 2f, size.height / 2f)
        drawCircle(
            color = if (connected) NyxCyan.copy(alpha = 0.12f * pulse) else Color.White.copy(alpha = 0.04f),
            radius = radius + 18.dp.toPx(),
            center = center,
        )
        drawCircle(
            color = Color.White.copy(alpha = 0.08f),
            radius = radius,
            center = center,
            style = Stroke(width = stroke),
        )
        when {
            connected -> drawCircle(
                brush = Brush.sweepGradient(listOf(NyxTeal, NyxCyan, NyxTeal)),
                radius = radius,
                center = center,
                style = Stroke(width = stroke, cap = StrokeCap.Round),
            )
            busy -> drawArc(
                brush = Brush.sweepGradient(listOf(NyxCyan, Color.Transparent, NyxTeal)),
                startAngle = sweep,
                sweepAngle = 110f,
                useCenter = false,
                topLeft = Offset(center.x - radius, center.y - radius),
                size = Size(radius * 2, radius * 2),
                style = Stroke(width = stroke, cap = StrokeCap.Round),
            )
            else -> drawArc(
                color = NyxMuted.copy(alpha = 0.45f),
                startAngle = -90f,
                sweepAngle = 360f,
                useCenter = false,
                topLeft = Offset(center.x - radius, center.y - radius),
                size = Size(radius * 2, radius * 2),
                style = Stroke(width = stroke * 0.6f),
            )
        }
        if (connected) {
            // simple check mark
            val c = center
            drawLine(NyxSuccess, Offset(c.x - 22, c.y), Offset(c.x - 6, c.y + 18), strokeWidth = 6f, cap = StrokeCap.Round)
            drawLine(NyxSuccess, Offset(c.x - 6, c.y + 18), Offset(c.x + 26, c.y - 16), strokeWidth = 6f, cap = StrokeCap.Round)
        }
    }
}

@Composable
private fun GlassCard(content: @Composable () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(20.dp))
            .background(NyxGlass)
            .border(1.dp, NyxCyan.copy(alpha = 0.18f), RoundedCornerShape(20.dp))
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
        content = { content() },
    )
}

@Composable
private fun Metric(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.labelLarge)
        Text(value, style = MaterialTheme.typography.titleLarge)
    }
}

private fun formatSession(startedAtMs: Long): String {
    if (startedAtMs <= 0) return "—"
    val sec = ((System.currentTimeMillis() - startedAtMs) / 1000).coerceAtLeast(0)
    val h = sec / 3600
    val m = (sec % 3600) / 60
    val s = sec % 60
    return "%02d:%02d:%02d".format(h, m, s)
}

private fun formatMbps(value: Double?): String {
    val v = value ?: 0.0
    return String.format(java.util.Locale("ru", "RU"), "%.1f Mbps", v)
}

internal fun formatTransport(raw: String): String {
    val t = raw.lowercase()
    return when {
        t.contains("quic") && t.contains("443") -> "QUIC UDP / 443"
        t.contains("quic") -> "QUIC"
        t.contains("tls") -> "TLS"
        raw.isBlank() -> "—"
        else -> raw
    }
}

private fun stateStatusLabel(state: ConnectionState): String = when (state) {
    ConnectionState.Disconnected -> "Отключено"
    ConnectionState.Preparing -> "Подготовка..."
    ConnectionState.Connecting -> "Соединение с сервером..."
    ConnectionState.Authenticating -> "Авторизация..."
    ConnectionState.WaitingForConfig -> "Получение конфигурации..."
    ConnectionState.ConfiguringTunnel -> "Настройка туннеля..."
    ConnectionState.Connected -> "Подключено"
    ConnectionState.Reconnecting -> "Переподключение..."
    ConnectionState.Disconnecting -> "Отключение..."
    ConnectionState.Error -> "Ошибка"
}
