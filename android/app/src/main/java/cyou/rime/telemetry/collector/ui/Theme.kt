package cyou.rime.telemetry.collector.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/** The panel's dark palette. */
private val Dark = darkColorScheme(
    primary = Color(0xFFEEEEEB),
    onPrimary = Color(0xFF111213),
    primaryContainer = Color(0xFF242628),
    onPrimaryContainer = Color(0xFFEEEEEB),
    secondary = Color(0xFFAAA9A4),
    onSecondary = Color(0xFF111213),
    secondaryContainer = Color(0xFF242628),
    onSecondaryContainer = Color(0xFFEEEEEB),
    background = Color(0xFF111213),
    onBackground = Color(0xFFEEEEEB),
    surface = Color(0xFF111213),
    onSurface = Color(0xFFEEEEEB),
    surfaceVariant = Color(0xFF242628),
    onSurfaceVariant = Color(0xFFAAA9A4),
    surfaceContainerLowest = Color(0xFF111213),
    surfaceContainerLow = Color(0xFF1A1B1D),
    surfaceContainer = Color(0xFF1E2022),
    surfaceContainerHigh = Color(0xFF242628),
    surfaceContainerHighest = Color(0xFF2C2F31),
    outline = Color(0xFF3A3D40),
    outlineVariant = Color(0xFF2C2F31),
)

/** The panel's light palette. */
private val Light = lightColorScheme(
    primary = Color(0xFF181918),
    onPrimary = Color(0xFFF5F5F2),
    primaryContainer = Color(0xFFE9EAE5),
    onPrimaryContainer = Color(0xFF181918),
    secondary = Color(0xFF63645F),
    onSecondary = Color(0xFFF5F5F2),
    secondaryContainer = Color(0xFFE9EAE5),
    onSecondaryContainer = Color(0xFF181918),
    background = Color(0xFFF5F5F2),
    onBackground = Color(0xFF181918),
    surface = Color(0xFFF5F5F2),
    onSurface = Color(0xFF181918),
    surfaceVariant = Color(0xFFE9EAE5),
    onSurfaceVariant = Color(0xFF63645F),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFFFFFFF),
    surfaceContainer = Color(0xFFFFFFFF),
    surfaceContainerHigh = Color(0xFFE9EAE5),
    surfaceContainerHighest = Color(0xFFDFE0DA),
    outline = Color(0xFFC4C5BF),
    outlineVariant = Color(0xFFDFE0DA),
)

/** The app's look: the panel's palette, light or dark as the phone is set. */
@Composable
fun TelemetryTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (isSystemInDarkTheme()) Dark else Light, content = content)
}
