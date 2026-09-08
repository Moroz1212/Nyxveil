package ru.nyxveil.android.ui.servers

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import ru.nyxveil.android.catalog.LocationUi
import ru.nyxveil.android.ui.theme.NyxBackgroundBrush
import ru.nyxveil.android.ui.theme.NyxCyan
import ru.nyxveil.android.ui.theme.NyxGlass
import ru.nyxveil.android.ui.theme.NyxMuted
import ru.nyxveil.android.ui.theme.NyxOnDark
import ru.nyxveil.android.ui.theme.NyxTeal

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServersScreen(
    locations: List<LocationUi>,
    selectedLocationId: String,
    refreshing: Boolean,
    error: String?,
    onSelect: (String) -> Unit,
    onRefresh: () -> Unit,
) {
    Column(
        Modifier
            .fillMaxSize()
            .background(NyxBackgroundBrush)
            .padding(16.dp),
    ) {
        Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Серверы", style = MaterialTheme.typography.headlineMedium, color = NyxOnDark)
            TextButton(onClick = onRefresh, enabled = !refreshing) {
                Text(if (refreshing) "Обновление…" else "Обновить", color = NyxTeal)
            }
        }
        Text(
            text = "Локации (не физические узлы).",
            style = MaterialTheme.typography.bodyMedium,
            color = NyxMuted,
        )
        if (!error.isNullOrBlank()) {
            Text(
                text = error,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
        PullToRefreshBox(
            isRefreshing = refreshing,
            onRefresh = onRefresh,
            modifier = Modifier.fillMaxSize().padding(top = 8.dp),
        ) {
            LazyColumn(
                contentPadding = PaddingValues(vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                items(locations, key = { it.locationId }) { loc ->
                    val selected = loc.locationId == selectedLocationId
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clip(RoundedCornerShape(18.dp))
                            .background(NyxGlass)
                            .border(
                                width = if (selected) 1.5.dp else 1.dp,
                                color = if (selected) NyxCyan.copy(alpha = 0.85f) else NyxCyan.copy(alpha = 0.12f),
                                shape = RoundedCornerShape(18.dp),
                            )
                            .clickable { onSelect(loc.locationId) }
                            .padding(16.dp),
                    ) {
                        Text(loc.displayName, style = MaterialTheme.typography.titleLarge, color = NyxOnDark)
                        Text(
                            text = listOf(loc.city, loc.country).filter { it.isNotBlank() }.joinToString(", "),
                            style = MaterialTheme.typography.bodyMedium,
                            color = NyxMuted,
                        )
                        if (selected) {
                            Text("Выбрано", style = MaterialTheme.typography.labelLarge, color = NyxTeal)
                        }
                    }
                }
            }
        }
    }
}
