package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.DaemonStatus
import com.sshcustom.vpnchain.domain.NetSpeed
import com.sshcustom.vpnchain.domain.TunnelState
import top.yukonga.miuix.kmp.basic.ButtonDefaults
import top.yukonga.miuix.kmp.basic.Card
import top.yukonga.miuix.kmp.basic.Text
import top.yukonga.miuix.kmp.basic.TextButton
import top.yukonga.miuix.kmp.theme.MiuixTheme

@Composable
fun HomeScreen(
    status: DaemonStatus,
    netSpeed: NetSpeed,
    wanIp: String,
    tunnelState: TunnelState,
    hasRoot: Boolean,
    isLoading: Boolean,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRestart: () -> Unit,
    onReload: () -> Unit,
    paddingValues: PaddingValues,
) {
    if (!hasRoot) { NoRootScreen(paddingValues); return }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(paddingValues)
            .padding(horizontal = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Spacer(Modifier.height(4.dp))
        StatusCard(status, tunnelState)
        ControlButtons(tunnelState, isLoading, onStart, onStop, onRestart, onReload)
        // Info grid — all cards fixed 90.dp height for equal sizing
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            InfoCard("WAN", Modifier.weight(1f).height(90.dp)) {
                Text(
                    text = wanIp.ifBlank { "—" },
                    fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface,
                    fontFamily = FontFamily.Monospace, maxLines = 2,
                )
            }
            InfoCard("Net Speed", Modifier.weight(1f).height(90.dp)) {
                Text("↑ ${"%.1f".format(netSpeed.upKbs)} KB/s", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
                Text("↓ ${"%.1f".format(netSpeed.downKbs)} KB/s", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
            }
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            InfoCard("Resources", Modifier.weight(1f).height(90.dp)) {
                Text("Mem: ${"%.1f".format(status.memRssMb)} MB", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
                Text("CPU: ${"%.1f".format(status.cpuPercent)}%", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
            }
            InfoCard("Pool", Modifier.weight(1f).height(90.dp)) {
                Text("${status.channelPoolAvail}/${status.channelPoolSize} ch", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
                Text("${status.activeConnections} active", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
            }
        }
        Spacer(Modifier.height(8.dp))
    }
}

@Composable
private fun StatusCard(status: DaemonStatus, state: TunnelState) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                val dotColor = when (state) {
                    is TunnelState.Connected -> Color(0xFF4CAF50)
                    is TunnelState.Starting  -> Color(0xFFFFC107)
                    is TunnelState.Stopping  -> Color(0xFFFF9800)
                    is TunnelState.Error     -> Color(0xFFCF6679)
                    else                     -> Color(0xFF757575)
                }
                Box(Modifier.size(12.dp).clip(CircleShape).background(dotColor))
                Text(
                    text = when (state) {
                        is TunnelState.Connected -> "Running"
                        is TunnelState.Starting  -> "Connecting…"
                        is TunnelState.Stopping  -> "Stopping…"
                        is TunnelState.Error     -> "Error"
                        else                     -> "Stopped"
                    },
                    fontSize = 20.sp, fontWeight = FontWeight.SemiBold,
                    color = MiuixTheme.colorScheme.onSurface,
                )
            }
            if (status.connected) {
                Text(
                    text = "Uptime: ${formatUptime(status.uptimeSeconds)}",
                    fontSize = 14.sp, fontFamily = FontFamily.Monospace,
                    color = MiuixTheme.colorScheme.primary,
                )
                Text(
                    text = "${status.sshMode.uppercase()} · ${status.networkMode.uppercase()}",
                    fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                )
            }
            if (state is TunnelState.Error) {
                Text(state.message, fontSize = 12.sp, color = MiuixTheme.colorScheme.error)
            }
        }
    }
}

@Composable
private fun ControlButtons(
    state: TunnelState, isLoading: Boolean,
    onStart: () -> Unit, onStop: () -> Unit, onRestart: () -> Unit, onReload: () -> Unit,
) {
    val stopped = state is TunnelState.Stopped || state is TunnelState.Error
    if (stopped) {
        TextButton(
            text = if (isLoading) "Starting…" else "Start",
            onClick = onStart, enabled = !isLoading,
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.textButtonColorsPrimary(),
        )
    } else {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            TextButton("Reload", onReload, enabled = !isLoading, modifier = Modifier.weight(1f))
            TextButton(
                text = if (isLoading) "…" else "Stop", onClick = onStop,
                enabled = !isLoading, modifier = Modifier.weight(1f),
                colors = ButtonDefaults.textButtonColors(
                    color = MiuixTheme.colorScheme.error,
                    textColor = Color.White,
                    disabledColor = MiuixTheme.colorScheme.disabledSecondaryVariant,
                    disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                ),
            )
            TextButton("Restart", onRestart, enabled = !isLoading, modifier = Modifier.weight(1f))
        }
    }
}

@Composable
private fun InfoCard(title: String, modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Card(modifier = modifier) {
        Column(Modifier.padding(12.dp).fillMaxSize(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(title, fontSize = 11.sp, fontWeight = FontWeight.Medium, color = MiuixTheme.colorScheme.onSurfaceVariantActions)
            content()
        }
    }
}

@Composable
private fun NoRootScreen(paddingValues: PaddingValues) {
    Box(Modifier.fillMaxSize().padding(paddingValues), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("🔒", fontSize = 48.sp)
            Text("Root not found", fontSize = 20.sp, fontWeight = FontWeight.SemiBold, color = MiuixTheme.colorScheme.onSurface)
            Text(
                "Grant root in KernelSU → Superuser tab.",
                fontSize = 14.sp, color = MiuixTheme.colorScheme.onSurfaceVariantActions,
            )
        }
    }
}

private fun formatUptime(s: Long): String {
    if (s < 60) return "${s}s"
    val h = s / 3600; val m = (s % 3600) / 60; val sec = s % 60
    return if (h > 0) "%02d:%02d:%02d".format(h, m, sec) else "%02d:%02d".format(m, sec)
}
