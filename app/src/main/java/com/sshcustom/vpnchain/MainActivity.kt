package com.sshcustom.vpnchain

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.*
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

/**
 * Root composable.
 *
 * Architecture note:
 * - Each screen owns its own MiuixScrollBehavior + TopAppBar.
 * - The Scaffold here only hosts the shared NavigationBar at the bottom.
 * - This lets every screen have independent collapsing-header behaviour
 *   and avoids the nestedScroll connection needing to cross screen boundaries.
 */
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
    val activeProfileId by vm.activeProfileId.collectAsState()   // now a StateFlow
    val settings        by vm.settings.collectAsState()
    val hasRoot         by vm.hasRoot.collectAsState()
    val isLoading       by vm.isLoading.collectAsState()

    var selectedTab by remember { mutableIntStateOf(0) }

    val navItems = listOf(
        NavigationItem(label = "Home",     icon = MiuixIcons.Useful.NavigatorSwitch),
        NavigationItem(label = "Profiles", icon = MiuixIcons.Useful.Personal),
        NavigationItem(label = "Settings", icon = MiuixIcons.Useful.Settings),
        NavigationItem(label = "Logs",     icon = MiuixIcons.Useful.Order),
    )

    // Scaffold here only provides the bottom NavigationBar + popup host.
    // Each screen composable is responsible for its own TopAppBar + Scaffold.
    Scaffold(
        topBar = {}, // screens own their TopAppBar
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
                tunnelState = tunnelState, hasRoot = hasRoot, isLoading = isLoading,
                onStart = vm::startTunnel, onStop = vm::stopTunnel,
                onRestart = vm::restartTunnel, onReload = vm::reloadConfig,
                bottomPadding = bottomPadding,
            )
            1 -> ProfilesScreen(
                profiles = profiles, activeProfileId = activeProfileId,
                onSelectProfile = vm::selectProfile, onSaveProfile = vm::saveProfile,
                onDeleteProfile = vm::deleteProfile,
                bottomPadding = bottomPadding,
            )
            2 -> SettingsScreen(
                settings = settings, onSettingsChange = vm::updateSettings,
                onForceCleanup = vm::forceCleanup,
                needsRestart = vm.settingsNeedRestart,
                appVersion = BuildConfig.VERSION_NAME,
                bottomPadding = bottomPadding,
            )
            3 -> LogsScreen(
                logText = logText, activeLog = activeLog,
                onSwitchLog = vm::switchLog,
                onClear = vm::clearLog,
                onRefresh = vm::refreshLog,
                bottomPadding = bottomPadding,
            )
        }
    }
}
