package com.sshcustom.vpnchain.ui.screens

import androidx.compose.animation.core.*
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.PaddingValues
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.DaemonStatus
import com.sshcustom.vpnchain.domain.NetSpeed
import com.sshcustom.vpnchain.domain.TunnelState
import top.yukonga.miuix.kmp.basic.*
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
    bottomPadding: PaddingValues,
) {
    if (!hasRoot) { NoRootScreen(bottomPadding); return }

    // Each screen owns its scroll behaviour so TopAppBar collapses independently
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
            item { ControlButtons(tunnelState, isLoading, onStart, onStop, onRestart, onReload) }
            item { InfoGrid(status, netSpeed, wanIp) }
        }
    }
}

// ── Status card ─────────────────────────────────────────────────────────────

@Composable
private fun StatusCard(status: DaemonStatus, state: TunnelState) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(
            modifier = Modifier.padding(horizontal = 18.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                StatusDot(state)
                Text(
                    text = when (state) {
                        is TunnelState.Connected -> "Running"
                        is TunnelState.Starting  -> "Connecting…"
                        is TunnelState.Stopping  -> "Stopping…"
                        is TunnelState.Error     -> "Error"
                        else                     -> "Stopped"
                    },
                    fontSize = 22.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MiuixTheme.colorScheme.onSurface,
                )
            }

            if (status.connected) {
                // Live uptime — updates every second via the status flow
                Text(
                    text = "Uptime  ${formatUptime(status.uptimeSeconds)}",
                    fontSize = 14.sp,
                    fontFamily = FontFamily.Monospace,
                    color = MiuixTheme.colorScheme.primary,
                )
                Text(
                    text = "${status.sshMode.uppercase()}  ·  ${status.networkMode.uppercase()}  ·  v${status.version}",
                    fontSize = 12.sp,
                    color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                )
            }

            if (state is TunnelState.Error) {
                Text(
                    text = state.message,
                    fontSize = 12.sp,
                    color = MiuixTheme.colorScheme.error,
                )
            }
        }
    }
}

/** Animated dot: pulses while Starting, solid while Running, grey when Stopped. */
@Composable
private fun StatusDot(state: TunnelState) {
    val dotColor = when (state) {
        is TunnelState.Connected -> Color(0xFF4CAF50)
        is TunnelState.Starting  -> Color(0xFFFFC107)
        is TunnelState.Stopping  -> Color(0xFFFF9800)
        is TunnelState.Error     -> Color(0xFFCF6679)
        else                     -> Color(0xFF757575)
    }

    val pulse = state is TunnelState.Starting || state is TunnelState.Stopping
    val infiniteTransition = rememberInfiniteTransition(label = "dot_pulse")
    val scale by infiniteTransition.animateFloat(
        initialValue = 1f,
        targetValue = if (pulse) 1.35f else 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(600, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "pulse_scale",
    )

    Box(
        modifier = Modifier
            .size(14.dp)
            .scale(scale)
            .clip(CircleShape)
            .background(dotColor),
    )
}

// ── Control buttons ──────────────────────────────────────────────────────────

@Composable
private fun ControlButtons(
    state: TunnelState,
    isLoading: Boolean,
    onStart: () -> Unit,
    onStop: () -> Unit,
    onRestart: () -> Unit,
    onReload: () -> Unit,
) {
    val stopped = state is TunnelState.Stopped || state is TunnelState.Error
    if (stopped) {
        TextButton(
            text = if (isLoading) "Starting…" else "Start",
            onClick = onStart,
            enabled = !isLoading,
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.textButtonColorsPrimary(),
        )
    } else {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            TextButton(
                text = "Reload", onClick = onReload,
                enabled = !isLoading, modifier = Modifier.weight(1f),
            )
            TextButton(
                text = if (isLoading) "…" else "Stop",
                onClick = onStop,
                enabled = !isLoading,
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.textButtonColors(
                    color              = MiuixTheme.colorScheme.error,
                    textColor          = Color.White,
                    disabledColor      = MiuixTheme.colorScheme.disabledSecondaryVariant,
                    disabledTextColor  = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                ),
            )
            TextButton(
                text = "Restart", onClick = onRestart,
                enabled = !isLoading, modifier = Modifier.weight(1f),
            )
        }
    }
}

// ── Info grid ────────────────────────────────────────────────────────────────

@Composable
private fun InfoGrid(status: DaemonStatus, netSpeed: NetSpeed, wanIp: String) {
    // Two rows of two equal-height cards
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            InfoCard("WAN", Modifier.weight(1f)) {
                val displayIp = wanIp.ifBlank { "—" }
                Text(
                    text = displayIp,
                    fontSize = 12.sp,
                    color = MiuixTheme.colorScheme.onSurface,
                    fontFamily = FontFamily.Monospace,
                    maxLines = 2,
                )
            }
            InfoCard("Net Speed", Modifier.weight(1f)) {
                Text("↑  ${"%.1f".format(netSpeed.upKbs)} KB/s",   fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
                Text("↓  ${"%.1f".format(netSpeed.downKbs)} KB/s", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
            }
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            InfoCard("Resources", Modifier.weight(1f)) {
                Text("Mem  ${"%.1f".format(status.memRssMb)} MB", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
                Text("CPU  ${"%.1f".format(status.cpuPercent)}%",  fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
            }
            InfoCard("Pool", Modifier.weight(1f)) {
                Text("${status.channelPoolAvail}/${status.channelPoolSize} channels", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
                Text("${status.activeConnections} active",           fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurface)
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
    Card(modifier = modifier.height(90.dp)) {
        Column(
            modifier = Modifier.padding(12.dp).fillMaxSize(),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = title,
                fontSize = 11.sp,
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
                fontSize = 20.sp, fontWeight = FontWeight.SemiBold,
                color = MiuixTheme.colorScheme.onSurface,
            )
            Text(
                text = "Open KernelSU → Superuser tab\n→ find SSHCustom → Allow",
                fontSize = 14.sp,
                color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
            )
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
