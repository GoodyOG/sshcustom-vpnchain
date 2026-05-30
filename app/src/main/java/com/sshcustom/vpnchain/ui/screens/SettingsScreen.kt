package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.AppSettings
import top.yukonga.miuix.kmp.basic.ButtonDefaults
import top.yukonga.miuix.kmp.basic.Card
import top.yukonga.miuix.kmp.basic.SmallTitle
import top.yukonga.miuix.kmp.basic.Text
import top.yukonga.miuix.kmp.basic.TextButton
import top.yukonga.miuix.kmp.basic.TextField
import top.yukonga.miuix.kmp.extra.SuperArrow
import top.yukonga.miuix.kmp.extra.SuperSwitch
import top.yukonga.miuix.kmp.theme.MiuixTheme

@Composable
fun SettingsScreen(
    settings: AppSettings,
    onSettingsChange: (AppSettings) -> Unit,
    onForceCleanup: () -> Unit,
    needsRestart: Boolean,
    appVersion: String,
    paddingValues: PaddingValues,
) {
    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(paddingValues),
    ) {
        if (needsRestart) {
            Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 6.dp)) {
                Text(
                    "⚠️  Settings changed — restart tunnel to apply",
                    fontSize = 13.sp, color = MiuixTheme.colorScheme.error,
                    modifier = Modifier.padding(14.dp),
                )
            }
        }

        // ── Ports ─────────────────────────────────────────────────────────────
        SmallTitle("Ports")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            PortField("Redirect Port", settings.redirPort.toString()) { v ->
                v.toIntOrNull()?.takeIf { it in 1024..65535 }
                    ?.let { onSettingsChange(settings.copy(redirPort = it)) }
            }
            PortField("TPROXY Port", settings.tproxyPort.toString()) { v ->
                v.toIntOrNull()?.takeIf { it in 1024..65535 }
                    ?.let { onSettingsChange(settings.copy(tproxyPort = it)) }
            }
            PortField("SOCKS5 Port", settings.socksPort.toString()) { v ->
                v.toIntOrNull()?.takeIf { it in 1024..65535 }
                    ?.let { onSettingsChange(settings.copy(socksPort = it)) }
            }
        }

        // ── Traffic Mode ──────────────────────────────────────────────────────
        SmallTitle("Traffic Mode")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            listOf(
                "redirect"  to "Redirect (TCP, most compatible)",
                "tproxy"    to "TPROXY (TCP+UDP, needs kernel)",
                "tun"       to "TUN (via tun2proxy)",
                "tun_udpgw" to "TUN + UDPGW (real UDP)",
            ).forEach { (mode, label) ->
                SuperArrow(
                    title = label,
                    rightActions = {
                        if (settings.networkMode == mode)
                            Text("✓", color = MiuixTheme.colorScheme.primary, fontSize = MiuixTheme.textStyles.body2.fontSize)
                    },
                    onClick = { onSettingsChange(settings.copy(networkMode = mode)) },
                )
            }
        }

        // ── Proxy Behaviour ───────────────────────────────────────────────────
        SmallTitle("Proxy Behaviour")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            SuperSwitch(
                checked = settings.quic == "disable",
                onCheckedChange = { onSettingsChange(settings.copy(quic = if (it) "disable" else "enable")) },
                title = "Block QUIC",
                summary = "Drop UDP 443/80 — forces TCP through tunnel",
            )
            SuperSwitch(
                checked = settings.proxyTcp,
                onCheckedChange = { onSettingsChange(settings.copy(proxyTcp = it)) },
                title = "Proxy TCP",
            )
            SuperSwitch(
                checked = settings.proxyUdp,
                onCheckedChange = { onSettingsChange(settings.copy(proxyUdp = it)) },
                title = "Proxy UDP",
                summary = "Only effective in TPROXY / TUN modes",
            )
        }

        // ── Speed Boost ───────────────────────────────────────────────────────
        SmallTitle("Speed Boost")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            SuperSwitch(
                checked = settings.channelPool,
                onCheckedChange = { onSettingsChange(settings.copy(channelPool = it)) },
                title = "Channel Pool",
                summary = "Pre-warm SSH channels — reduces first-connection latency",
            )
            SuperSwitch(
                checked = settings.bbrEnabled,
                onCheckedChange = { onSettingsChange(settings.copy(bbrEnabled = it)) },
                title = "BBR Congestion Control",
                summary = "Enable BBR if kernel supports it",
            )
            SuperSwitch(
                checked = settings.tcpBufferTuning,
                onCheckedChange = { onSettingsChange(settings.copy(tcpBufferTuning = it)) },
                title = "TCP Buffer Tuning",
                summary = "Maximize buffer sizes for high throughput",
            )
        }

        // ── DNS Hijack ────────────────────────────────────────────────────────
        SmallTitle("DNS Hijack")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            SuperSwitch(
                checked = settings.dnsHijackTcp,
                onCheckedChange = { onSettingsChange(settings.copy(dnsHijackTcp = it)) },
                title = "Hijack DNS TCP",
            )
            SuperSwitch(
                checked = settings.dnsHijackUdp,
                onCheckedChange = { onSettingsChange(settings.copy(dnsHijackUdp = it)) },
                title = "Hijack DNS UDP",
            )
            listOf("redirect" to "Redirect", "tproxy" to "TPROXY", "disable" to "Disable").forEach { (m, l) ->
                SuperArrow(
                    title = "DNS Mode: $l",
                    rightActions = {
                        if (settings.dnsHijackMode == m)
                            Text("✓", color = MiuixTheme.colorScheme.primary, fontSize = MiuixTheme.textStyles.body2.fontSize)
                    },
                    onClick = { onSettingsChange(settings.copy(dnsHijackMode = m)) },
                )
            }
        }

        // ── IPv6 ──────────────────────────────────────────────────────────────
        SmallTitle("IPv6")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            SuperSwitch(
                checked = !settings.ipv6,
                onCheckedChange = { onSettingsChange(settings.copy(ipv6 = !it)) },
                title = "Disable IPv6",
                summary = "Recommended while tunnel is active",
            )
        }

        // ── Developer ─────────────────────────────────────────────────────────
        SmallTitle("Developer")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 12.dp)) {
            SuperArrow(
                title = "Force iptables cleanup",
                summary = "Runs ssh.iptables disable — clears all rules",
                onClick = onForceCleanup,
            )
        }

        // ── About ─────────────────────────────────────────────────────────────
        SmallTitle("About")
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp, 0.dp, 12.dp, 24.dp)) {
            SuperArrow(title = "App version", rightActions = {
                Text(appVersion, color = MiuixTheme.colorScheme.onSurfaceVariantActions, fontSize = MiuixTheme.textStyles.body2.fontSize)
            }, onClick = {})
            SuperArrow(title = "Module data", rightActions = {
                Text("/data/adb/sshcustom", color = MiuixTheme.colorScheme.onSurfaceVariantActions, fontSize = 11.sp)
            }, onClick = {})
            SuperArrow(title = "VPN Chain", rightActions = {
                Text("Coming soon", color = MiuixTheme.colorScheme.onSurfaceVariantActions, fontSize = MiuixTheme.textStyles.body2.fontSize)
            }, onClick = {})
        }
    }
}

@Composable
private fun PortField(label: String, value: String, onDone: (String) -> Unit) {
    var text by remember(value) { mutableStateOf(value) }
    TextField(
        value = text,
        onValueChange = { text = it.filter { c -> c.isDigit() }; onDone(text) },
        label = label, singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
        modifier = Modifier.fillMaxWidth().padding(12.dp, 4.dp),
    )
}
