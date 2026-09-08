package ru.nyxveil.android.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.sp

val NyxNavy = Color(0xFF070B14)
val NyxNavyElevated = Color(0xFF0E1626)
val NyxNavySurface = Color(0xCC121C30)
val NyxGlass = Color(0x33182235)
val NyxTeal = Color(0xFF2DD4BF)
val NyxCyan = Color(0xFF22D3EE)
val NyxOnDark = Color(0xFFE8EEF7)
val NyxMuted = Color(0xFF94A3B8)
val NyxError = Color(0xFFF87171)
val NyxSuccess = Color(0xFF34D399)
val NyxGlow = Color(0x6622D3EE)

val NyxBackgroundBrush = Brush.verticalGradient(
    colors = listOf(Color(0xFF050814), Color(0xFF0B1220), Color(0xFF101A2E)),
)

private val NyxColors = darkColorScheme(
    primary = NyxTeal,
    secondary = NyxCyan,
    background = NyxNavy,
    surface = NyxNavyElevated,
    onPrimary = Color(0xFF042F2E),
    onSecondary = Color(0xFF083344),
    onBackground = NyxOnDark,
    onSurface = NyxOnDark,
    error = NyxError,
)

private val NyxTypography = androidx.compose.material3.Typography(
    displayLarge = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.Bold,
        fontSize = 40.sp,
        letterSpacing = (-0.5).sp,
        color = NyxOnDark,
    ),
    displaySmall = TextStyle(
        fontFamily = FontFamily.Serif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 28.sp,
        color = NyxOnDark,
    ),
    headlineMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 22.sp,
        color = NyxOnDark,
    ),
    titleLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 18.sp,
        color = NyxOnDark,
    ),
    titleMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Medium,
        fontSize = 16.sp,
        color = NyxOnDark,
    ),
    bodyLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Normal,
        fontSize = 16.sp,
        color = NyxOnDark,
    ),
    bodyMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Normal,
        fontSize = 14.sp,
        color = NyxMuted,
    ),
    labelLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Medium,
        fontSize = 12.sp,
        letterSpacing = 0.8.sp,
        color = NyxMuted,
    ),
)

@Composable
fun NyxveilTheme(content: @Composable () -> Unit) {
    @Suppress("UNUSED_VARIABLE")
    val dark = isSystemInDarkTheme()
    MaterialTheme(
        colorScheme = NyxColors,
        typography = NyxTypography,
        content = content,
    )
}
