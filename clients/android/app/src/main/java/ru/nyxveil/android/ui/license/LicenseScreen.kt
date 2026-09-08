package ru.nyxveil.android.ui.license

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import ru.nyxveil.android.R
import ru.nyxveil.android.ui.theme.NyxBackgroundBrush
import ru.nyxveil.android.ui.theme.NyxCyan
import ru.nyxveil.android.ui.theme.NyxGlass
import ru.nyxveil.android.ui.theme.NyxMuted
import ru.nyxveil.android.ui.theme.NyxOnDark
import ru.nyxveil.android.ui.theme.NyxTeal

@Composable
fun LicenseScreen(
    loading: Boolean,
    error: String?,
    onContinue: (String) -> Unit,
) {
    var key by remember { mutableStateOf("") }
    Box(
        Modifier
            .fillMaxSize()
            .background(NyxBackgroundBrush)
            .padding(24.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(NyxGlass, RoundedCornerShape(24.dp))
                .border(1.dp, NyxCyan.copy(alpha = 0.2f), RoundedCornerShape(24.dp))
                .padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text("Nyxveil", style = MaterialTheme.typography.displaySmall, color = NyxOnDark)
            Text(
                stringResource(R.string.tagline).uppercase(),
                style = MaterialTheme.typography.labelLarge,
                color = NyxCyan,
            )
            Spacer(Modifier.height(8.dp))
            Text("Добро пожаловать в Nyxveil", style = MaterialTheme.typography.titleLarge)
            Text(
                "Введите лицензионный ключ",
                style = MaterialTheme.typography.bodyMedium,
                color = NyxMuted,
            )
            OutlinedTextField(
                value = key,
                onValueChange = { key = it },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
                label = { Text("Лицензионный ключ") },
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = NyxTeal,
                    unfocusedBorderColor = NyxMuted.copy(alpha = 0.4f),
                    focusedTextColor = NyxOnDark,
                    unfocusedTextColor = NyxOnDark,
                    cursorColor = NyxTeal,
                ),
            )
            if (!error.isNullOrBlank()) {
                Text(error, color = MaterialTheme.colorScheme.error)
            }
            Button(
                onClick = { onContinue(key) },
                enabled = !loading && key.isNotBlank(),
                modifier = Modifier.fillMaxWidth().height(52.dp),
                shape = RoundedCornerShape(16.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = NyxTeal,
                    contentColor = Color(0xFF042F2E),
                ),
            ) {
                Text(if (loading) "Проверка…" else "Продолжить")
            }
        }
    }
}
