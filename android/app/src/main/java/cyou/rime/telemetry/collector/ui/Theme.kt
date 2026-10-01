package cyou.rime.telemetry.collector.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

val Background = Color(0xFF111213)
val Foreground = Color(0xFFEEEEEB)
val Muted = Color(0xFFAAA9A4)
val Soft = Color(0xFF242628)
private val Line = Color(0xFF3A3D40)

/** The app's one look: the dark palette the Now screen and the panel share. */
private val Colors = darkColorScheme(
    primary = Foreground,
    onPrimary = Background,
    primaryContainer = Soft,
    onPrimaryContainer = Foreground,
    secondary = Muted,
    onSecondary = Background,
    secondaryContainer = Soft,
    onSecondaryContainer = Foreground,
    background = Background,
    onBackground = Foreground,
    surface = Background,
    onSurface = Foreground,
    surfaceVariant = Soft,
    onSurfaceVariant = Muted,
    surfaceContainerLowest = Background,
    surfaceContainerLow = Color(0xFF1A1B1D),
    surfaceContainer = Color(0xFF1E2022),
    surfaceContainerHigh = Soft,
    surfaceContainerHighest = Color(0xFF2C2F31),
    outline = Line,
    outlineVariant = Color(0xFF2C2F31),
)

@Composable
fun TelemetryTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = Colors, content = content)
}
