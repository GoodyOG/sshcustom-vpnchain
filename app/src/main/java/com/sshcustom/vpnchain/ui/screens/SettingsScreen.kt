package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.sshcustom.vpnchain.domain.AppSettings
import top.yukonga.miuix.kmp.basic.ButtonDefaults
import top.yukonga.miuix.kmp.basic.Card
import top.yukonga.miuix.kmp.basic.SmallTitle
import top.yukonga.miuix.kmp.basic.Text
import top.yukonga.miuix.kmp.basic.TextButton
import top.yukonga.miuix.kmp.basic.TextField
import top.yukonga.miuix.kmp.preference.ArrowPreference
import top.yukonga.miuix.kmp.preference.SwitchPreference
import top.yukonga.miuix.kmp.theme.MiuixTheme
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.sp

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
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(paddingValues),
        verticalArrangement = Arrangement.spacedBy(0.dp)
    ) {
        // Restart banner
        if (needsRestart) {
            Card(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 6.dp)
            ) {
                Text(
                    text = "⚠️  Settings changed — restart tunnel to apply",
                    fontSize = 13.sp,
                    color = MiuixTheme.colorScheme.error,
                    modifier = Modifier.padding(14.dp),
                )
            }
        }

        // ── Ports ─────────────────────────────────────────────────────────────
        SmallTitle(text = "Ports", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            PortPreference("Redirect Port", settings.redirPort.toString()) { v ->
                v.toIntOrNull()?.takeIf { it in 1024..65535 }?.let {
                    onSettingsChange(settings.copy(redirPort = it))
                }
            }
            PortPreference("TPROXY Port", settings.tproxyPort.toString()) { v ->
                v.toIntOrNull()?.takeIf { it in 1024..65535 }?.let {
                    onSettingsChange(settings.copy(tproxyPort = it))
                }
            }
            PortPreference("SOCKS5 Port", settings.socksPort.toString()) { v ->
                v.toIntOrNull()?.takeIf { it in 1024..65535 }?.let {
                    onSettingsChange(settings.copy(socksPort = it))
                }
            }
        }

        // ── Traffic Mode ──────────────────────────────────────────────────────
        SmallTitle(text = "Traffic Mode", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            listOf(
                "redirect"  to "Redirect (TCP only, most compatible)",
                "tproxy"    to "TPROXY (TCP+UDP, needs kernel support)",
                "tun"       to "TUN (via tun2proxy)",
                "tun_udpgw" to "TUN + UDPGW (real UDP tunneling)",
            ).forEach { (mode, label) ->
                ArrowPreference(
                    title = label,
                    endActions = {
                        if (settings.networkMode == mode) {
                            Text(
                                text = "✓",
                                color = MiuixTheme.colorScheme.primary,
                                fontSize = MiuixTheme.textStyles.body2.fontSize,
                            )
                        }
                    },
                    onClick = { onSettingsChange(settings.copy(networkMode = mode)) },
                )
            }
        }

        // ── Proxy Behaviour ───────────────────────────────────────────────────
        SmallTitle(text = "Proxy Behaviour", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            SwitchPreference(
                title = "Block QUIC",
                summary = "Drop UDP 443/80 — forces TCP tunnel compatibility",
                checked = settings.quic == "disable",
                onCheckedChange = { onSettingsChange(settings.copy(quic = if (it) "disable" else "enable")) },
            )
            SwitchPreference(
                title = "Proxy TCP",
                checked = settings.proxyTcp,
                onCheckedChange = { onSettingsChange(settings.copy(proxyTcp = it)) },
            )
            SwitchPreference(
                title = "Proxy UDP",
                summary = "Only effective in TPROXY/TUN modes",
                checked = settings.proxyUdp,
                onCheckedChange = { onSettingsChange(settings.copy(proxyUdp = it)) },
            )
        }

        // ── Speed Boost ───────────────────────────────────────────────────────
        SmallTitle(text = "Speed Boost", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            SwitchPreference(
                title = "Channel Pool",
                summary = "Pre-warm SSH channels — eliminates per-connection latency",
                checked = settings.channelPool,
                onCheckedChange = { onSettingsChange(settings.copy(channelPool = it)) },
            )
            SwitchPreference(
                title = "BBR Congestion Control",
                summary = "Enable BBR if kernel supports it",
                checked = settings.bbrEnabled,
                onCheckedChange = { onSettingsChange(settings.copy(bbrEnabled = it)) },
            )
            SwitchPreference(
                title = "TCP Buffer Tuning",
                summary = "Maximize buffer sizes for high throughput",
                checked = settings.tcpBufferTuning,
                onCheckedChange = { onSettingsChange(settings.copy(tcpBufferTuning = it)) },
            )
        }

        // ── DNS Hijack ────────────────────────────────────────────────────────
        SmallTitle(text = "DNS Hijack", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            SwitchPreference(
                title = "Hijack DNS TCP",
                checked = settings.dnsHijackTcp,
                onCheckedChange = { onSettingsChange(settings.copy(dnsHijackTcp = it)) },
            )
            SwitchPreference(
                title = "Hijack DNS UDP",
                checked = settings.dnsHijackUdp,
                onCheckedChange = { onSettingsChange(settings.copy(dnsHijackUdp = it)) },
            )
            listOf("redirect" to "Redirect", "tproxy" to "TPROXY", "disable" to "Disable").forEach { (m, l) ->
                ArrowPreference(
                    title = "DNS Mode: $l",
                    endActions = {
                        if (settings.dnsHijackMode == m) {
                            Text("✓", color = MiuixTheme.colorScheme.primary,
                                fontSize = MiuixTheme.textStyles.body2.fontSize)
                        }
                    },
                    onClick = { onSettingsChange(settings.copy(dnsHijackMode = m)) },
                )
            }
        }

        // ── IPv6 ──────────────────────────────────────────────────────────────
        SmallTitle(text = "IPv6", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            SwitchPreference(
                title = "Disable IPv6",
                summary = "Disable IPv6 system-wide while tunnel is active (recommended)",
                checked = !settings.ipv6,
                onCheckedChange = { onSettingsChange(settings.copy(ipv6 = !it)) },
            )
        }

        // ── Developer ─────────────────────────────────────────────────────────
        SmallTitle(text = "Developer", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 12.dp)) {
            ArrowPreference(
                title = "Force iptables cleanup",
                summary = "Runs ssh.iptables disable — clears all rules even if stuck",
                onClick = onForceCleanup,
            )
        }

        // ── About ─────────────────────────────────────────────────────────────
        SmallTitle(text = "About", modifier = Modifier.padding(horizontal = 12.dp))
        Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 24.dp)) {
            ArrowPreference(title = "Version", endActions = {
                Text(appVersion, color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                    fontSize = MiuixTheme.textStyles.body2.fontSize)
            }, onClick = {})
            ArrowPreference(title = "Data path", endActions = {
                Text("/data/adb/sshcustom", color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                    fontSize = 11.sp)
            }, onClick = {})
            ArrowPreference(title = "VPN Chain", endActions = {
                Text("Coming soon", color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                    fontSize = MiuixTheme.textStyles.body2.fontSize)
            }, onClick = {})
        }
    }
}

@Composable
private fun PortPreference(label: String, value: String, onDone: (String) -> Unit) {
    var text by remember(value) { mutableStateOf(value) }
    TextField(
        value = text,
        onValueChange = {
            val digits = it.filter { c -> c.isDigit() }
            text = digits
            onDone(digits)
        },
        label = label,
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 4.dp),
    )
}
