package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.VpnChainState
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.extra.SuperDropdown
import top.yukonga.miuix.kmp.theme.MiuixTheme

private fun chainColor(s: VpnChainState): Color = when (s) {
    is VpnChainState.Connected  -> Color(0xFF4CAF50)
    is VpnChainState.Connecting -> Color(0xFFFFC107)
    is VpnChainState.Error      -> Color(0xFFCF6679)
    else                        -> Color(0xFF9E9E9E)
}

private fun chainLabel(s: VpnChainState): String = when (s) {
    is VpnChainState.Connected  -> "Connected"
    is VpnChainState.Connecting -> "Connecting…"
    is VpnChainState.Error      -> "Error"
    else                        -> "Off"
}

@Composable
fun VpnChainScreen(
    sshConnected: Boolean,
    configs: List<String>,
    selectedConfig: String,
    state: VpnChainState,
    exitIp: String,
    vpnUser: String,
    vpnPass: String,
    busy: Boolean,
    onSelectConfig: (String) -> Unit,
    onSaveCreds: (String, String) -> Unit,
    onRefreshConfigs: () -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    bottomPadding: PaddingValues,
) {
    val scrollBehavior = MiuixScrollBehavior()

    LaunchedEffect(Unit) { onRefreshConfigs() }

    var user by remember(vpnUser) { mutableStateOf(vpnUser) }
    var pass by remember(vpnPass) { mutableStateOf(vpnPass) }
    var showPwd by remember { mutableStateOf(false) }

    val connected = state is VpnChainState.Connected
    val connecting = state is VpnChainState.Connecting || busy

    Scaffold(
        topBar = { TopAppBar(title = "VPN Chain", scrollBehavior = scrollBehavior) },
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
            // Status card
            item {
                Card(Modifier.fillMaxWidth()) {
                    Column(
                        Modifier.padding(horizontal = 18.dp, vertical = 16.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Text(
                            text = "OpenVPN  ·  ${chainLabel(state)}",
                            fontSize = 22.sp,
                            fontWeight = FontWeight.SemiBold,
                            color = chainColor(state),
                        )
                        Text(
                            text = "Exit IP  ${if (connected) exitIp else "—"}",
                            fontSize = 14.sp,
                            fontFamily = FontFamily.Monospace,
                            color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                        )
                        if (state is VpnChainState.Error) {
                            Text(state.message, fontSize = 13.sp, color = MiuixTheme.colorScheme.error)
                        }
                    }
                }
            }

            // Requirement note
            item {
                Card(Modifier.fillMaxWidth()) {
                    Text(
                        "Chains OpenVPN through the SSH tunnel — your exit IP becomes the VPN's.\n" +
                            "• Connect SSHCustom (Home) first.\n" +
                            "• Use a TCP Windscribe config.\n" +
                            "• Put .ovpn files in /data/adb/sshcustom/vpnchain/configs/",
                        fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                        modifier = Modifier.padding(14.dp),
                    )
                }
            }

            // Config selector
            item { SmallTitle("Config") }
            item {
                Card(Modifier.fillMaxWidth()) {
                    if (configs.isEmpty()) {
                        Text(
                            "No .ovpn configs found.\nPaste them into …/vpnchain/configs/ then tap Refresh.",
                            fontSize = 13.sp,
                            color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                            modifier = Modifier.padding(14.dp),
                        )
                    } else {
                        SuperDropdown(
                            title = "OpenVPN config",
                            items = configs,
                            selectedIndex = configs.indexOf(selectedConfig).coerceAtLeast(0),
                            onSelectedIndexChange = { i -> onSelectConfig(configs[i]) },
                        )
                    }
                }
            }
            item {
                TextButton(
                    text = "Refresh configs",
                    onClick = onRefreshConfigs,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            // Credentials
            item { SmallTitle("Windscribe Credentials") }
            item {
                TextField(
                    value = user,
                    onValueChange = { user = it.trim() },
                    label = "OpenVPN Username",
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            item {
                TextField(
                    value = pass,
                    onValueChange = { pass = it },
                    label = "OpenVPN Password",
                    singleLine = true,
                    visualTransformation = if (showPwd) VisualTransformation.None else PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(
                        text = if (showPwd) "Hide" else "Show",
                        onClick = { showPwd = !showPwd },
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(
                        text = "Save",
                        onClick = { onSaveCreds(user, pass) },
                        modifier = Modifier.weight(1f),
                    )
                }
            }

            // Connect / Disconnect
            item { Spacer(Modifier.height(2.dp)) }
            item {
                if (connected || connecting) {
                    TextButton(
                        text = if (connecting && !connected) "Connecting…" else "Disconnect",
                        onClick = onDisconnect,
                        enabled = !connecting || connected,
                        modifier = Modifier.fillMaxWidth(),
                        colors = ButtonDefaults.textButtonColors(
                            color             = MiuixTheme.colorScheme.error,
                            textColor         = Color.White,
                            disabledColor     = MiuixTheme.colorScheme.disabledSecondaryVariant,
                            disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                        ),
                    )
                } else {
                    TextButton(
                        text = "Connect VPN Chain",
                        onClick = onConnect,
                        enabled = sshConnected && configs.isNotEmpty(),
                        modifier = Modifier.fillMaxWidth(),
                        colors = ButtonDefaults.textButtonColorsPrimary(),
                    )
                }
            }
            if (!sshConnected) {
                item {
                    Text(
                        "SSHCustom is not connected — start it on the Home tab first.",
                        fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.error,
                        modifier = Modifier.padding(horizontal = 4.dp),
                    )
                }
            }
        }
    }
}
