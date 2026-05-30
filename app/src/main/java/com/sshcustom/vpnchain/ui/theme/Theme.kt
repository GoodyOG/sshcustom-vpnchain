package com.sshcustom.vpnchain.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.Composable
import top.yukonga.miuix.kmp.theme.MiuixTheme
import top.yukonga.miuix.kmp.theme.darkColorScheme
import top.yukonga.miuix.kmp.theme.lightColorScheme

/**
 * SSHCustom app theme — wraps MiuixTheme.
 * Uses system dark/light mode. No Material3 anywhere.
 */
@Composable
fun SSHCustomTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit
) {
    val colors = if (darkTheme) darkColorScheme() else lightColorScheme()
    MiuixTheme(
        colors = colors,
        content = content
    )
}
