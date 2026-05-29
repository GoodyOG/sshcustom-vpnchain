package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
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

@Composable
fun HomeScreen(
    status: DaemonStatus,
    netSpeed: NetSpeed,
    wanIp: String,
    tunnelState: TunnelState,
    hasRoot: Boolean,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRestart: () -> Unit,
    onReload: () -> Unit,
) {
    if (!hasRoot) {
        NoRootScreen()
        return
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        // Status card
        StatusCard(status, tunnelState)

        // Control buttons
        ControlButtons(tunnelState, onStart, onStop, onRestart, onReload)

        // Info cards grid
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            WanCard(wanIp, Modifier.weight(1f))
            NetSpeedCard(netSpeed, Modifier.weight(1f))
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            ResourceCard(status, Modifier.weight(1f))
            PoolCard(status, Modifier.weight(1f))
        }
    }
}

@Composable
private fun StatusCard(status: DaemonStatus, state: TunnelState) {
    val cardColor = MaterialTheme.colorScheme.surface
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(containerColor = cardColor),
        elevation = CardDefaults.cardElevation(defaultElevation = 4.dp)
    ) {
        Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                val dotColor = when (state) {
                    is TunnelState.Connected -> Color(0xFF4CAF50)
                    is TunnelState.Starting  -> Color(0xFFFFC107)
                    else                     -> Color(0xFF757575)
                }
                Box(Modifier.size(12.dp).clip(CircleShape).background(dotColor))
                Text(
                    text = when (state) {
                        is TunnelState.Connected -> "Running"
                        is TunnelState.Starting  -> "Connecting..."
                        is TunnelState.Stopping  -> "Stopping..."
                        is TunnelState.Error     -> "Error"
                        else                     -> "Stopped"
                    },
                    fontSize = 20.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface
                )
            }

            if (status.connected) {
                UptimeText(status.uptimeSeconds)
                Text(
                    text = "${status.sshMode.uppercase()} · ${status.networkMode.uppercase()}",
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f)
                )
            }

            if (state is TunnelState.Error) {
                Text(state.message, fontSize = 12.sp, color = MaterialTheme.colorScheme.error)
            }
        }
    }
}

@Composable
private fun UptimeText(seconds: Long) {
    val h = seconds / 3600
    val m = (seconds % 3600) / 60
    val s = seconds % 60
    val text = if (seconds < 60) "${s}s"
    else if (seconds < 3600) "%02d:%02d".format(m, s)
    else "%02d:%02d:%02d".format(h, m, s)

    Text(
        text = "Uptime: $text",
        fontSize = 14.sp,
        fontFamily = FontFamily.Monospace,
        color = MaterialTheme.colorScheme.primary
    )
}

@Composable
private fun ControlButtons(
    state: TunnelState,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRestart: () -> Unit,
    onReload: () -> Unit,
) {
    if (state is TunnelState.Stopped || state is TunnelState.Error) {
        Button(
            onClick = onStart,
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(12.dp)
        ) {
            Text("Start", fontWeight = FontWeight.SemiBold)
        }
    } else {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            OutlinedButton(onClick = onReload, modifier = Modifier.weight(1f)) { Text("Reload") }
            Button(
                onClick = onStop,
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)
            ) { Text("Stop") }
            OutlinedButton(onClick = onRestart, modifier = Modifier.weight(1f)) { Text("Restart") }
        }
    }
}

@Composable
private fun InfoCard(title: String, modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Card(
        modifier = modifier,
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(title, fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f), fontWeight = FontWeight.Medium)
            content()
        }
    }
}

@Composable private fun WanCard(ip: String, modifier: Modifier) = InfoCard("WAN", modifier) {
    Text(ip.ifBlank { "—" }, fontSize = 13.sp, fontWeight = FontWeight.Medium, color = MaterialTheme.colorScheme.onSurface)
}

@Composable private fun NetSpeedCard(speed: NetSpeed, modifier: Modifier) = InfoCard("Net Speed", modifier) {
    Text("↑ ${"%.1f".format(speed.upKbs)} KB/s", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
    Text("↓ ${"%.1f".format(speed.downKbs)} KB/s", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
}

@Composable private fun ResourceCard(status: DaemonStatus, modifier: Modifier) = InfoCard("Resources", modifier) {
    Text("Mem: ${"%.1f".format(status.memRssMb)} MB", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
    Text("CPU: ${"%.1f".format(status.cpuPercent)}%", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
}

@Composable private fun PoolCard(status: DaemonStatus, modifier: Modifier) = InfoCard("Pool", modifier) {
    Text("${status.channelPoolAvail}/${status.channelPoolSize} channels", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
    Text("${status.activeConnections} active", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
}

@Composable
private fun NoRootScreen() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("🔒", fontSize = 48.sp)
            Text("Root not found", fontSize = 20.sp, fontWeight = FontWeight.SemiBold)
            Text("This app requires root access via Magisk, KernelSU, or APatch.",
                fontSize = 14.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f))
        }
    }
}
