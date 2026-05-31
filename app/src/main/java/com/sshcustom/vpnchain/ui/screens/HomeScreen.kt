package com.sshcustom.vpnchain.ui.screens

import androidx.compose.animation.core.*
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.DaemonStatus
import com.sshcustom.vpnchain.domain.NetSpeed
import com.sshcustom.vpnchain.domain.TunnelState
import kotlinx.coroutines.delay
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.theme.MiuixTheme

@Composable
fun HomeScreen(
    status: DaemonStatus,
    netSpeed: NetSpeed,
    wanIp: String,
    tunnelState: TunnelState,
    pendingAction: String?,
    hasRoot: Boolean,
    isLoading: Boolean,
    latencyGoogle: Int,
    latencyCloudflare: Int,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRestart: () -> Unit,
    onPingLatency: () -> Unit,
    bottomPadding: PaddingValues,
) {
    if (!hasRoot) { NoRootScreen(bottomPadding); return }

    val scrollBehavior = MiuixScrollBehavior()

    Scaffold(
        topBar = {
            TopAppBar(
                title = "Home",
                scrollBehavior = scrollBehavior,
            )
        },
    ) { innerPadding ->
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .nestedScroll(scrollBehavior.nestedScrollConnection),
            contentPadding = PaddingValues(
                start = 12.dp, end = 12.dp,
                top = innerPadding.calculateTopPadding() + 8.dp,
                bottom = bottomPadding.calculateBottomPadding() + 16.dp,
            ),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item { StatusCard(status, tunnelState) }
            item { ControlButtons(tunnelState, pendingAction, isLoading, onStart, onStop, onRestart) }
            item { InfoGrid(status, netSpeed, wanIp) }
            item { LatencyCard(latencyGoogle, latencyCloudflare, onPingLatency) }
        }
    }
}

// ── Status helpers ────────────────────────────────────────────────────────────

private fun statusColor(state: TunnelState): Color = when (state) {
    is TunnelState.Connected -> Color(0xFF4CAF50)
    is TunnelState.Starting  -> Color(0xFFFFC107)
    is TunnelState.Stopping  -> Color(0xFFFF9800)
    is TunnelState.Error     -> Color(0xFFCF6679)
    else                     -> Color(0xFF9E9E9E)
}

private fun statusLabel(state: TunnelState): String = when (state) {
    is TunnelState.Connected -> "Running"
    is TunnelState.Starting  -> "Starting"
    is TunnelState.Stopping  -> "Stopping"
    is TunnelState.Error     -> "Error"
    else                     -> "Stopped"
}

private fun statusSubtitle(state: TunnelState): String = when (state) {
    is TunnelState.Starting -> "Connecting to server…"
    is TunnelState.Stopping -> "Shutting down…"
    is TunnelState.Error    -> "Tap Start to retry"
    else                    -> "Tunnel is off"
}

// ── Status card ─────────────────────────────────────────────────────────────

@Composable
private fun StatusCard(status: DaemonStatus, state: TunnelState) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(
            modifier = Modifier.padding(horizontal = 18.dp, vertical = 18.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(14.dp),
            ) {
                StatusIcon(state)
                Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    Text(
                        text = statusLabel(state),
                        fontSize = 24.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = statusColor(state),
                    )
                    if (status.connected) {
                        Text(
                            text = "Uptime  ${formatUptime(status.uptimeSeconds)}",
                            fontSize = 14.sp,
                            fontFamily = FontFamily.Monospace,
                            color = MiuixTheme.colorScheme.primary,
                        )
                    } else {
                        Text(
                            text = statusSubtitle(state),
                            fontSize = 13.sp,
                            color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                        )
                    }
                }
            }

            if (status.connected) {
                Text(
                    text = "${status.sshMode.uppercase()}  ·  ${status.networkMode.uppercase()}  ·  v${status.version}",
                    fontSize = 14.sp,
                    color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                )
            }

            if (state is TunnelState.Error) {
                Text(
                    text = state.message,
                    fontSize = 14.sp,
                    color = MiuixTheme.colorScheme.error,
                )
            }
        }
    }
}

/**
 * Box-style status icon. A filled disc when Running, a hollow ring when
 * Stopped/Error, and a rotating arc (spinner) while Starting/Stopping.
 */
@Composable
private fun StatusIcon(state: TunnelState) {
    val color = statusColor(state)
    val spinning = state is TunnelState.Starting || state is TunnelState.Stopping

    val transition = rememberInfiniteTransition(label = "status_spin")
    val angle by transition.animateFloat(
        initialValue = 0f,
        targetValue = 360f,
        animationSpec = infiniteRepeatable(
            animation = tween(900, easing = LinearEasing),
        ),
        label = "angle",
    )

    Canvas(modifier = Modifier.size(34.dp)) {
        val strokeW = 3.dp.toPx()
        val d = size.minDimension - strokeW
        val topLeft = Offset((size.width - d) / 2f, (size.height - d) / 2f)
        val arcSize = Size(d, d)
        when {
            spinning -> {
                drawArc(color.copy(alpha = 0.25f), 0f, 360f, false, topLeft, arcSize,
                    style = Stroke(strokeW, cap = StrokeCap.Round))
                drawArc(color, angle, 100f, false, topLeft, arcSize,
                    style = Stroke(strokeW, cap = StrokeCap.Round))
            }
            state is TunnelState.Connected -> {
                drawCircle(color, radius = d / 2f, center = Offset(size.width / 2f, size.height / 2f))
            }
            else -> {
                drawArc(color, 0f, 360f, false, topLeft, arcSize, style = Stroke(strokeW))
            }
        }
    }
}

// ── Control buttons ──────────────────────────────────────────────────────────

/**
 * A small braille spinner that animates while [active]. Returns a frame char +
 * spacing, or an empty string when inactive. Always call it unconditionally.
 */
@Composable
private fun spinnerFrame(active: Boolean): String {
    val frames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
    var idx by remember { mutableIntStateOf(0) }
    LaunchedEffect(active) {
        if (active) {
            while (true) {
                delay(90)
                idx = (idx + 1) % frames.length
            }
        }
    }
    return if (active) "${frames[idx]}  " else ""
}

@Composable
private fun ControlButtons(
    state: TunnelState,
    pendingAction: String?,
    isLoading: Boolean,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRestart: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        // Status label when an action is in flight
        if (pendingAction != null) {
            val spin = spinnerFrame(true)
            val label = when (pendingAction) {
                "stop" -> "Stopping\u2026"
                "restart" -> "Restarting\u2026"
                else -> "Starting\u2026"
            }
            Text(
                text = "$spin$label",
                fontSize = 13.sp,
                color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                modifier = Modifier.padding(horizontal = 4.dp),
            )
        }

        // All three buttons always visible and always clickable
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            TextButton(
                text = "Start",
                onClick = onStart,
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.textButtonColorsPrimary(),
            )
            TextButton(
                text = "Stop",
                onClick = onStop,
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.textButtonColors(
                    color             = MiuixTheme.colorScheme.error,
                    textColor         = Color.White,
                    disabledColor     = MiuixTheme.colorScheme.disabledSecondaryVariant,
                    disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                ),
            )
            TextButton(
                text = "Restart",
                onClick = onRestart,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

// ── Info grid ────────────────────────────────────────────────────────────────

@Composable
private fun InfoGrid(status: DaemonStatus, netSpeed: NetSpeed, wanIp: String) {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            InfoCard("WAN", Modifier.weight(1f)) {
                Text(
                    text       = wanIp.ifBlank { "—" },
                    fontSize   = 14.sp,
                    fontWeight = FontWeight.Bold,
                    color      = MiuixTheme.colorScheme.onSurface,
                    fontFamily = FontFamily.Monospace,
                    maxLines   = 2,
                )
            }
            InfoCard("Net Speed", Modifier.weight(1f)) {
                Text("↑  ${"%.1f".format(netSpeed.upKbs)} KB/s",   fontSize = 14.sp, fontWeight = FontWeight.Bold, color = MiuixTheme.colorScheme.onSurface)
                Text("↓  ${"%.1f".format(netSpeed.downKbs)} KB/s", fontSize = 14.sp, fontWeight = FontWeight.Bold, color = MiuixTheme.colorScheme.onSurface)
            }
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            InfoCard("Resources", Modifier.weight(1f)) {
                Text("Mem  ${"%.1f".format(status.memRssMb)} MB", fontSize = 14.sp, fontWeight = FontWeight.Bold, color = MiuixTheme.colorScheme.onSurface)
                Text("CPU  ${"%.1f".format(status.cpuPercent)}%",  fontSize = 14.sp, fontWeight = FontWeight.Bold, color = MiuixTheme.colorScheme.onSurface)
            }
            InfoCard("Connections", Modifier.weight(1f)) {
                Text("${status.activeConnections} active", fontSize = 14.sp, fontWeight = FontWeight.Bold, color = MiuixTheme.colorScheme.onSurface)
                Text(if (status.connected) "tunnel up" else "tunnel down", fontSize = 14.sp, fontWeight = FontWeight.Bold, color = MiuixTheme.colorScheme.onSurface)
            }
        }
    }
}

/** Fixed-height card used in the info grid — all four are the same height. */
@Composable
private fun InfoCard(
    title: String,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    Card(modifier = modifier.height(100.dp)) {
        Column(
            modifier = Modifier.padding(12.dp).fillMaxSize(),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = title,
                fontSize = 13.sp,
                fontWeight = FontWeight.Medium,
                color = MiuixTheme.colorScheme.onSurfaceVariantActions,
            )
            content()
        }
    }
}

// ── No-root screen ───────────────────────────────────────────────────────────

@Composable
private fun NoRootScreen(bottomPadding: PaddingValues) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .padding(bottom = bottomPadding.calculateBottomPadding()),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Text("🔒", fontSize = 52.sp)
            Text(
                text = "Root access not found",
                fontSize = 22.sp, fontWeight = FontWeight.SemiBold,
                color = MiuixTheme.colorScheme.onSurface,
            )
            Text(
                text = "Open KernelSU → Superuser tab\n→ find SSHCustom → Allow",
                fontSize = 16.sp,
                color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
            )
        }
    }
}

// ── Latency card ─────────────────────────────────────────────────────────────

@Composable
private fun LatencyCard(latencyGoogle: Int, latencyCloudflare: Int, onPing: () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(12.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = "Latency",
                    fontSize = 13.sp,
                    fontWeight = FontWeight.Medium,
                    color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                )
                TextButton(
                    text = "Ping",
                    onClick = onPing,
                )
            }
            Spacer(modifier = Modifier.height(6.dp))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(24.dp),
            ) {
                Text(
                    text = "Google  ${if (latencyGoogle >= 0) "${latencyGoogle} ms" else "—"}",
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                    color = MiuixTheme.colorScheme.onSurface,
                )
                Text(
                    text = "Cloudflare  ${if (latencyCloudflare >= 0) "${latencyCloudflare} ms" else "—"}",
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                    color = MiuixTheme.colorScheme.onSurface,
                )
            }
        }
    }
}

// ── Uptime formatter ─────────────────────────────────────────────────────────

private fun formatUptime(s: Long): String {
    if (s < 60) return "${s}s"
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    return if (h > 0) "%02d:%02d:%02d".format(h, m, sec) else "%02d:%02d".format(m, sec)
}
