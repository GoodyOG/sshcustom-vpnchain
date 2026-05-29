package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.AppSettings

@Composable
fun SettingsScreen(
    settings: AppSettings,
    onSettingsChange: (AppSettings) -> Unit,
    onForceCleanup: () -> Unit,
    appVersion: String,
) {
    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        SettingsCard("Ports") {
            PortField("Redirect Port", settings.redirPort.toString()) {
                onSettingsChange(settings.copy(redirPort = it.toIntOrNull() ?: settings.redirPort))
            }
            PortField("TPROXY Port", settings.tproxyPort.toString()) {
                onSettingsChange(settings.copy(tproxyPort = it.toIntOrNull() ?: settings.tproxyPort))
            }
            PortField("SOCKS5 Port", settings.socksPort.toString()) {
                onSettingsChange(settings.copy(socksPort = it.toIntOrNull() ?: settings.socksPort))
            }
        }

        SettingsCard("Traffic Mode") {
            Text("Network Mode", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.7f))
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                listOf("redirect", "tproxy", "tun", "tun_udpgw").forEach { mode ->
                    FilterChip(
                        selected = settings.networkMode == mode,
                        onClick = { onSettingsChange(settings.copy(networkMode = mode)) },
                        label = { Text(mode, fontSize = 11.sp) }
                    )
                }
            }
        }

        SettingsCard("Proxy Behaviour") {
            ToggleSetting("Block QUIC (force TCP)", settings.quic == "disable",
                subtitle = "Blocks UDP 443/80 — forces apps to use TCP for tunnel compatibility") {
                onSettingsChange(settings.copy(quic = if (it) "disable" else "enable"))
            }
            ToggleSetting("Proxy TCP", settings.proxyTcp) {
                onSettingsChange(settings.copy(proxyTcp = it))
            }
            ToggleSetting("Proxy UDP", settings.proxyUdp,
                subtitle = "Only available in TPROXY/TUN modes") {
                onSettingsChange(settings.copy(proxyUdp = it))
            }
        }

        SettingsCard("Speed Boost") {
            ToggleSetting("Channel Pool", settings.channelPool,
                subtitle = "Pre-warm SSH channels for maximum download speed") {
                onSettingsChange(settings.copy(channelPool = it))
            }
            ToggleSetting("BBR Congestion Control", settings.bbrEnabled,
                subtitle = "Enable BBR if kernel supports it") {
                onSettingsChange(settings.copy(bbrEnabled = it))
            }
            ToggleSetting("TCP Buffer Tuning", settings.tcpBufferTuning,
                subtitle = "Maximize buffer sizes for high throughput") {
                onSettingsChange(settings.copy(tcpBufferTuning = it))
            }
        }

        SettingsCard("DNS Hijack") {
            ToggleSetting("Hijack DNS TCP", settings.dnsHijackTcp) {
                onSettingsChange(settings.copy(dnsHijackTcp = it))
            }
            ToggleSetting("Hijack DNS UDP", settings.dnsHijackUdp) {
                onSettingsChange(settings.copy(dnsHijackUdp = it))
            }
            Text("DNS Hijack Mode", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.7f))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("redirect", "tproxy", "disable").forEach { m ->
                    FilterChip(
                        selected = settings.dnsHijackMode == m,
                        onClick = { onSettingsChange(settings.copy(dnsHijackMode = m)) },
                        label = { Text(m) }
                    )
                }
            }
        }

        SettingsCard("Developer") {
            Button(
                onClick = onForceCleanup,
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
                modifier = Modifier.fillMaxWidth()
            ) { Text("Force Cleanup iptables") }
            Text(
                text = "Runs ssh.iptables disable regardless of daemon state.\nUse if rules are stuck.",
                fontSize = 11.sp,
                color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f)
            )
        }

        SettingsCard("About") {
            Text("SSHCustom-VPNChain", fontWeight = FontWeight.SemiBold)
            Text("App version: $appVersion", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.7f))
            Text("Module data: /data/adb/sshcustom", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f))
            Text("VPN Chain: Coming soon", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f))
        }
    }
}

@Composable
private fun SettingsCard(title: String, content: @Composable ColumnScope.() -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(title, fontSize = 13.sp, fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.primary)
            content()
        }
    }
}

@Composable
private fun ToggleSetting(
    label: String,
    checked: Boolean,
    subtitle: String? = null,
    onToggle: (Boolean) -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Column(Modifier.weight(1f)) {
            Text(label, fontSize = 14.sp)
            if (subtitle != null) {
                Text(subtitle, fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f))
            }
        }
        Switch(checked = checked, onCheckedChange = onToggle)
    }
}

@Composable
private fun PortField(label: String, value: String, onValueChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label) },
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
        singleLine = true,
        modifier = Modifier.fillMaxWidth()
    )
}
