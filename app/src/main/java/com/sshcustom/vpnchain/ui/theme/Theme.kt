package com.sshcustom.vpnchain.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val DarkColors = darkColorScheme(
    primary         = Color(0xFF4CAF50),
    onPrimary       = Color.Black,
    secondary       = Color(0xFF81C784),
    background      = Color(0xFF0A0A0A),
    surface         = Color(0xFF1A1A1A),
    onBackground    = Color(0xFFE0E0E0),
    onSurface       = Color(0xFFE0E0E0),
    error           = Color(0xFFCF6679),
)

private val LightColors = lightColorScheme(
    primary         = Color(0xFF2E7D32),
    onPrimary       = Color.White,
    secondary       = Color(0xFF388E3C),
    background      = Color(0xFFF5F5F5),
    surface         = Color(0xFFFFFFFF),
    onBackground    = Color(0xFF1A1A1A),
    onSurface       = Color(0xFF1A1A1A),
)

@Composable
fun SSHCustomTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit
) {
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColors else LightColors,
        content = content
    )
}
