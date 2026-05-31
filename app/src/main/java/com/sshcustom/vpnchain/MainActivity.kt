package com.sshcustom.vpnchain

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.*
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.sshcustom.vpnchain.ui.screens.*
import com.sshcustom.vpnchain.ui.theme.SSHCustomTheme
import top.yukonga.miuix.kmp.basic.NavigationBar
import top.yukonga.miuix.kmp.basic.NavigationItem
import top.yukonga.miuix.kmp.basic.Scaffold
import top.yukonga.miuix.kmp.icon.MiuixIcons
import top.yukonga.miuix.kmp.icon.icons.useful.NavigatorSwitch
import top.yukonga.miuix.kmp.icon.icons.useful.Personal
import top.yukonga.miuix.kmp.icon.icons.useful.Settings
import top.yukonga.miuix.kmp.icon.icons.useful.Order

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { SSHCustomTheme { MainAppContent() } }
    }
}

// Custom shield icon for the VPN Chain tab (avoids depending on a specific
// miuix icon name). The NavigationBar tints it per selected state.
private val ShieldIcon: ImageVector by lazy {
    ImageVector.Builder(
        name = "shield", defaultWidth = 24.dp, defaultHeight = 24.dp,
        viewportWidth = 24f, viewportHeight = 24f,
    ).apply {
        path(fill = SolidColor(Color.Black)) {
            moveTo(12f, 1f)
            lineTo(3f, 5f)
            verticalLineToRelative(6f)
            curveTo(3f, 16.55f, 6.84f, 21.74f, 12f, 23f)
            curveTo(17.16f, 21.74f, 21f, 16.55f, 21f, 11f)
            verticalLineTo(5f)
            lineTo(12f, 1f)
            close()
        }
    }.build()
}

@Composable
fun MainAppContent() {
    val vm: MainViewModel = viewModel()
    val status          by vm.status.collectAsState()
    val tunnelState     by vm.tunnelState.collectAsState()
    val netSpeed        by vm.netSpeed.collectAsState()
    val wanIp           by vm.wanIp.collectAsState()
    val logText         by vm.logText.collectAsState()
    val activeLog       by vm.activeLog.collectAsState()
    val profiles        by vm.profiles.collectAsState()
    val activeProfileId by vm.activeProfileId.collectAsState()
    val settings        by vm.settings.collectAsState()
    val hasRoot         by vm.hasRoot.collectAsState()
    val isLoading       by vm.isLoading.collectAsState()
    val pendingAction   by vm.pendingAction.collectAsState()

    // VPN Chain
    val vpnConfigs      by vm.vpnConfigs.collectAsState()
    val selectedVpnCfg  by vm.selectedVpnConfig.collectAsState()
    val vpnChainState   by vm.vpnChainState.collectAsState()
    val chainExitIp     by vm.chainExitIp.collectAsState()
    val vpnBusy         by vm.vpnBusy.collectAsState()

    // Latency
    val latencyGoogle     by vm.latencyGoogle.collectAsState()
    val latencyCloudflare by vm.latencyCloudflare.collectAsState()

    var selectedTab by remember { mutableIntStateOf(0) }

    val navItems = listOf(
        NavigationItem(label = "Home",      icon = MiuixIcons.Useful.NavigatorSwitch),
        NavigationItem(label = "Profiles",  icon = MiuixIcons.Useful.Personal),
        NavigationItem(label = "VPN Chain", icon = ShieldIcon),
        NavigationItem(label = "Logs",      icon = MiuixIcons.Useful.Order),
        NavigationItem(label = "Settings",  icon = MiuixIcons.Useful.Settings),
    )

    Scaffold(
        topBar = {},
        bottomBar = {
            NavigationBar(
                items = navItems,
                selected = selectedTab,
                onClick = { selectedTab = it },
            )
        },
    ) { bottomPadding ->
        when (selectedTab) {
            0 -> HomeScreen(
                status = status, netSpeed = netSpeed, wanIp = wanIp,
                tunnelState = tunnelState, pendingAction = pendingAction,
                hasRoot = hasRoot, isLoading = isLoading,
                latencyGoogle = latencyGoogle,
                latencyCloudflare = latencyCloudflare,
                onStart = vm::startTunnel, onStop = vm::stopTunnel,
                onRestart = vm::restartTunnel,
                onPingLatency = vm::pingLatency,
                bottomPadding = bottomPadding,
            )
            1 -> ProfilesScreen(
                profiles = profiles, activeProfileId = activeProfileId,
                onSelectProfile = vm::selectProfile, onSaveProfile = vm::saveProfile,
                onDeleteProfile = vm::deleteProfile,
                bottomPadding = bottomPadding,
            )
            2 -> VpnChainScreen(
                sshConnected = status.connected,
                configs = vpnConfigs,
                selectedConfig = selectedVpnCfg,
                state = vpnChainState,
                exitIp = chainExitIp,
                busy = vpnBusy,
                onSelectConfig = vm::selectVpnConfig,
                onRefreshConfigs = vm::refreshVpnConfigs,
                onConnect = vm::startVpnChain,
                onDisconnect = vm::stopVpnChain,
                bottomPadding = bottomPadding,
            )
            3 -> LogsScreen(
                logText = logText, activeLog = activeLog,
                onSwitchLog = vm::switchLog,
                onClear = vm::clearLog,
                onRefresh = vm::refreshLog,
                bottomPadding = bottomPadding,
            )
            4 -> SettingsScreen(
                settings = settings, onSettingsChange = vm::updateSettings,
                needsRestart = vm.settingsNeedRestart,
                appVersion = BuildConfig.VERSION_NAME,
                bottomPadding = bottomPadding,
            )
        }
    }
}
