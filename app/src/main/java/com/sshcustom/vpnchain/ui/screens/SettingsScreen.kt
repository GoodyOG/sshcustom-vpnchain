package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.AppSettings
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.extra.SuperDropdown
import top.yukonga.miuix.kmp.extra.SuperSwitch
import top.yukonga.miuix.kmp.theme.MiuixTheme

// ── Dropdown option lists ─────────────────────────────────────────────────────

private val TRAFFIC_LABELS = listOf(
    "Redirect  —  TCP, most compatible",
    "TPROXY  —  TCP, kernel transparent proxy",
)
private val TRAFFIC_VALUES = listOf("redirect", "tproxy")
private fun trafficToIndex(v: String) = TRAFFIC_VALUES.indexOf(v).coerceAtLeast(0)
private fun indexToTraffic(i: Int)    = TRAFFIC_VALUES.getOrElse(i) { "redirect" }

private val DNS_MODE_LABELS = listOf("Redirect", "TPROXY", "Disable")
private val DNS_MODE_VALUES = listOf("redirect", "tproxy", "disable")
private fun dnsModeToIndex(v: String) = DNS_MODE_VALUES.indexOf(v).coerceAtLeast(0)
private fun indexToDnsMode(i: Int)    = DNS_MODE_VALUES.getOrElse(i) { "disable" }

// ── Screen ────────────────────────────────────────────────────────────────────

@Composable
fun SettingsScreen(
    settings: AppSettings,
    onSettingsChange: (AppSettings) -> Unit,
    needsRestart: Boolean,
    appVersion: String,
    bottomPadding: PaddingValues,
) {
    val scrollBehavior = MiuixScrollBehavior()

    Scaffold(
        topBar = { TopAppBar(title = "Settings", scrollBehavior = scrollBehavior) },
    ) { innerPadding ->
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .nestedScroll(scrollBehavior.nestedScrollConnection),
            contentPadding = PaddingValues(
                start  = 12.dp, end = 12.dp,
                top    = innerPadding.calculateTopPadding() + 4.dp,
                bottom = bottomPadding.calculateBottomPadding() + 16.dp,
            ),
        ) {

            // Restart warning
            if (needsRestart) {
                item {
                    Card(modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
                        Text(
                            "⚠  Settings changed — restart tunnel to apply",
                            fontSize = 13.sp,
                            color    = MiuixTheme.colorScheme.error,
                            modifier = Modifier.padding(14.dp),
                        )
                    }
                }
            }

            // ── Ports ─────────────────────────────────────────────────────────
            settingsSection("Ports") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        PortField("Redirect Port", settings.redirPort.toString(),
                            enabled = settings.networkMode == "redirect") { v ->
                            v.toIntOrNull()?.takeIf { it in 1024..65535 }
                                ?.let { onSettingsChange(settings.copy(redirPort = it)) }
                        }
                        PortField("TPROXY Port", settings.tproxyPort.toString(),
                            enabled = settings.networkMode == "tproxy") { v ->
                            v.toIntOrNull()?.takeIf { it in 1024..65535 }
                                ?.let { onSettingsChange(settings.copy(tproxyPort = it)) }
                        }
                        PortField("SOCKS5 Port", settings.socksPort.toString()) { v ->
                            v.toIntOrNull()?.takeIf { it in 1024..65535 }
                                ?.let { onSettingsChange(settings.copy(socksPort = it)) }
                        }
                    }
                }
            }

            // ── Traffic Mode ──────────────────────────────────────────────────
            settingsSection("Traffic Mode") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperDropdown(
                            title               = "Mode",
                            summary             = "How traffic is intercepted by the module",
                            items               = TRAFFIC_LABELS,
                            selectedIndex       = trafficToIndex(settings.networkMode),
                            onSelectedIndexChange = { i ->
                                onSettingsChange(settings.copy(networkMode = indexToTraffic(i)))
                            },
                        )
                    }
                }
            }

            // ── Proxy Behaviour ───────────────────────────────────────────────
            settingsSection("Proxy Behaviour") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperSwitch(
                            checked = settings.quic == "disable",
                            onCheckedChange = {
                                onSettingsChange(settings.copy(quic = if (it) "disable" else "enable"))
                            },
                            title   = "Block QUIC",
                            summary = "Drop UDP 443/80 — forces TCP through tunnel",
                        )
                        SuperSwitch(
                            checked         = settings.proxyTcp,
                            onCheckedChange = { onSettingsChange(settings.copy(proxyTcp = it)) },
                            title           = "Proxy TCP",
                        )
                        SuperSwitch(
                            checked         = settings.proxyUdp,
                            onCheckedChange = { onSettingsChange(settings.copy(proxyUdp = it)) },
                            enabled         = settings.networkMode == "tproxy",
                            title           = "Proxy UDP",
                            summary         = if (settings.networkMode == "tproxy")
                                                  "Tunnel UDP via TPROXY"
                                              else
                                                  "Not available in REDIRECT mode",
                        )
                    }
                }
            }

            // ── Performance ───────────────────────────────────────────────────
            settingsSection("Performance") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperSwitch(
                            checked         = settings.channelPool,
                            onCheckedChange = { onSettingsChange(settings.copy(channelPool = it)) },
                            title           = "Channel Pool",
                            summary         = "Pre-warm SSH channels — reduces per-connection latency",
                        )
                        SuperSwitch(
                            checked         = settings.tcpBufferTuning,
                            onCheckedChange = { onSettingsChange(settings.copy(tcpBufferTuning = it)) },
                            title           = "TCP Buffer Tuning",
                            summary         = "Maximize socket buffers for high throughput",
                        )
                    }
                }
            }

            // ── DNS Hijack ────────────────────────────────────────────────────
            settingsSection("DNS Hijack") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperSwitch(
                            checked         = settings.dnsHijackTcp,
                            onCheckedChange = { onSettingsChange(settings.copy(dnsHijackTcp = it)) },
                            title           = "DNS Hijack TCP",
                        )
                        SuperSwitch(
                            checked         = settings.dnsHijackUdp,
                            onCheckedChange = { onSettingsChange(settings.copy(dnsHijackUdp = it)) },
                            title           = "DNS Hijack UDP",
                        )
                        SuperDropdown(
                            title               = "DNS Hijack Mode",
                            items               = DNS_MODE_LABELS,
                            selectedIndex       = dnsModeToIndex(settings.dnsHijackMode),
                            onSelectedIndexChange = { i ->
                                onSettingsChange(settings.copy(dnsHijackMode = indexToDnsMode(i)))
                            },
                        )
                    }
                }
            }

            // ── IPv6 ──────────────────────────────────────────────────────────
            settingsSection("IPv6") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperSwitch(
                            checked         = !settings.ipv6,
                            onCheckedChange = { onSettingsChange(settings.copy(ipv6 = !it)) },
                            title           = "Disable IPv6",
                            summary         = "Recommended while tunnel is active",
                        )
                    }
                }
            }

            // ── Hotspot ───────────────────────────────────────────────────────
            settingsSection("Hotspot Sharing") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperSwitch(
                            checked         = settings.hotspotSharing,
                            onCheckedChange = { onSettingsChange(settings.copy(hotspotSharing = it)) },
                            title           = "Share tunnel via hotspot",
                            summary         = "Forward hotspot clients through the SSH tunnel. Requires restart.",
                        )
                    }
                }
            }

            // ── Boot behaviour ────────────────────────────────────────────────
            settingsSection("Boot Behaviour") {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        SuperSwitch(
                            checked         = settings.autostartTunnel,
                            onCheckedChange = { onSettingsChange(settings.copy(autostartTunnel = it)) },
                            title           = "Start tunnel on boot",
                            summary         = "OFF = daemon starts in idle mode only (WebUI accessible, no tunnel). ON = tunnel connects automatically after boot.",
                        )
                    }
                }
            }

            // ── About — Developer + Source rows are tappable links ────────────
            settingsSection("About") {
                item {
                    val context = LocalContext.current
                    val openUrl: (String) -> Unit = { url ->
                        runCatching {
                            val intent = android.content.Intent(
                                android.content.Intent.ACTION_VIEW,
                                android.net.Uri.parse(url),
                            ).apply {
                                addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
                            }
                            context.startActivity(intent)
                        }
                    }
                    Card(Modifier.fillMaxWidth()) {
                        BasicComponent(
                            title        = "Developer",
                            onClick      = { openUrl("https://github.com/GoodyOG") },
                            rightActions = {
                                Text("GoodyOG",
                                    color    = MiuixTheme.colorScheme.onSurfaceVariantActions,
                                    fontSize = MiuixTheme.textStyles.body2.fontSize)
                            },
                        )
                        BasicComponent(
                            title        = "Source",
                            onClick      = { openUrl("https://github.com/GoodyOG/sshcustom-vpnchain") },
                            rightActions = {
                                Text("github.com/GoodyOG/sshcustom-vpnchain",
                                    color    = MiuixTheme.colorScheme.onSurfaceVariantActions,
                                    fontSize = 11.sp)
                            },
                        )
                        BasicComponent(
                            title        = "App version",
                            rightActions = {
                                Text(appVersion,
                                    color    = MiuixTheme.colorScheme.onSurfaceVariantActions,
                                    fontSize = MiuixTheme.textStyles.body2.fontSize)
                            },
                        )
                        BasicComponent(
                            title        = "Module data",
                            rightActions = {
                                Text("/data/adb/sshcustom",
                                    color    = MiuixTheme.colorScheme.onSurfaceVariantActions,
                                    fontSize = 11.sp)
                            },
                        )
                    }
                }
            }
        }
    }
}

// ── Helpers ───────────────────────────────────────────────────────────────────

private fun LazyListScope.settingsSection(
    title: String,
    content: LazyListScope.() -> Unit,
) {
    item { SmallTitle(title) }
    content()
    item { Spacer(Modifier.height(4.dp)) }
}

@Composable
private fun PortField(label: String, value: String, enabled: Boolean = true, onDone: (String) -> Unit) {
    var text by remember(value) { mutableStateOf(value) }
    TextField(
        value           = text,
        onValueChange   = { text = it.filter { c -> c.isDigit() }; onDone(text) },
        label           = label,
        enabled         = enabled,
        singleLine      = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
        modifier        = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
    )
}
