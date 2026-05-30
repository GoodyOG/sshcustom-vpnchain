package com.sshcustom.vpnchain

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.lifecycle.viewmodel.compose.viewModel
import com.sshcustom.vpnchain.ui.screens.*
import com.sshcustom.vpnchain.ui.theme.SSHCustomTheme
import top.yukonga.miuix.kmp.basic.NavigationBar
import top.yukonga.miuix.kmp.basic.NavigationItem
import top.yukonga.miuix.kmp.basic.Scaffold
import top.yukonga.miuix.kmp.basic.TopAppBar
import top.yukonga.miuix.kmp.icon.MiuixIcons
import top.yukonga.miuix.kmp.icon.icons.useful.Info
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

@Composable
fun MainAppContent() {
    val vm: MainViewModel = viewModel()
    val status        by vm.status.collectAsState()
    val tunnelState   by vm.tunnelState.collectAsState()
    val netSpeed      by vm.netSpeed.collectAsState()
    val wanIp         by vm.wanIp.collectAsState()
    val logText       by vm.logText.collectAsState()
    val profiles      by vm.profiles.collectAsState()
    val settings      by vm.settings.collectAsState()
    val hasRoot       by vm.hasRoot.collectAsState()
    val isLoading     by vm.isLoading.collectAsState()

    var selectedTab by remember { mutableIntStateOf(0) }

    val tabs = listOf("Home", "Profiles", "Settings", "Logs")
    val navItems = listOf(
        NavigationItem(label = "Home",     icon = MiuixIcons.Useful.Info),
        NavigationItem(label = "Profiles", icon = MiuixIcons.Useful.Personal),
        NavigationItem(label = "Settings", icon = MiuixIcons.Useful.Settings),
        NavigationItem(label = "Logs",     icon = MiuixIcons.Useful.Order),
    )

    Scaffold(
        topBar = { TopAppBar(title = tabs[selectedTab]) },
        bottomBar = {
            NavigationBar(
                items = navItems,
                selected = selectedTab,
                onClick = { selectedTab = it },
                modifier = Modifier.windowInsetsPadding(WindowInsets.navigationBars)
            )
        }
    ) { paddingValues ->
        when (selectedTab) {
            0 -> HomeScreen(
                status = status, netSpeed = netSpeed, wanIp = wanIp,
                tunnelState = tunnelState, hasRoot = hasRoot, isLoading = isLoading,
                onStart = vm::startTunnel, onStop = vm::stopTunnel,
                onRestart = vm::restartTunnel, onReload = vm::reloadConfig,
                paddingValues = paddingValues,
            )
            1 -> ProfilesScreen(
                profiles = profiles, activeProfileId = vm.activeProfileId,
                onSelectProfile = vm::selectProfile, onSaveProfile = vm::saveProfile,
                onDeleteProfile = vm::deleteProfile, paddingValues = paddingValues,
            )
            2 -> SettingsScreen(
                settings = settings, onSettingsChange = vm::updateSettings,
                onForceCleanup = vm::forceCleanup,
                needsRestart = vm.settingsNeedRestart,
                appVersion = BuildConfig.VERSION_NAME,
                paddingValues = paddingValues,
            )
            3 -> LogsScreen(
                logText = logText, onClear = vm::clearLog,
                onRefresh = vm::refreshLog, paddingValues = paddingValues,
            )
        }
    }
}
