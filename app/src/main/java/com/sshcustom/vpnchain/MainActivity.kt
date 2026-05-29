package com.sshcustom.vpnchain

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.lifecycle.viewmodel.compose.viewModel
import com.sshcustom.vpnchain.ui.screens.*
import com.sshcustom.vpnchain.ui.theme.SSHCustomTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            SSHCustomTheme {
                SSHCustomApp()
            }
        }
    }
}

sealed class Screen(val route: String, val label: String, val icon: ImageVector) {
    object Home     : Screen("home",     "Home",     Icons.Default.Home)
    object Profiles : Screen("profiles", "Profiles", Icons.Default.Person)
    object Settings : Screen("settings", "Settings", Icons.Default.Settings)
    object Logs     : Screen("logs",     "Logs",     Icons.Default.Settings)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SSHCustomApp() {
    val vm: MainViewModel = viewModel()
    val status by vm.status.collectAsState()
    val tunnelState by vm.tunnelState.collectAsState()
    val netSpeed by vm.netSpeed.collectAsState()
    val wanIp by vm.wanIp.collectAsState()
    val logText by vm.logText.collectAsState()
    val profiles by vm.profiles.collectAsState()
    val settings by vm.settings.collectAsState()
    val hasRoot by vm.hasRoot.collectAsState()

    var currentScreen by remember { mutableStateOf<Screen>(Screen.Home) }

    val navItems = listOf(Screen.Home, Screen.Profiles, Screen.Settings, Screen.Logs)

    Scaffold(
        bottomBar = {
            NavigationBar {
                navItems.forEach { screen ->
                    NavigationBarItem(
                        selected = currentScreen == screen,
                        onClick = { currentScreen = screen },
                        icon = { Icon(screen.icon, contentDescription = screen.label) },
                        label = { Text(screen.label) }
                    )
                }
            }
        }
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when (currentScreen) {
                Screen.Home -> HomeScreen(
                    status = status,
                    netSpeed = netSpeed,
                    wanIp = wanIp,
                    tunnelState = tunnelState,
                    hasRoot = hasRoot,
                    onStart = vm::startTunnel,
                    onStop = vm::stopTunnel,
                    onRestart = vm::restartTunnel,
                    onReload = vm::reloadConfig,
                )
                Screen.Profiles -> ProfilesScreen(
                    profiles = profiles,
                    activeProfileId = vm.activeProfileId,
                    onSelectProfile = vm::selectProfile,
                    onSaveProfile = vm::saveProfile,
                    onDeleteProfile = vm::deleteProfile,
                )
                Screen.Settings -> SettingsScreen(
                    settings = settings,
                    onSettingsChange = vm::updateSettings,
                    onForceCleanup = vm::forceCleanup,
                    appVersion = "2.0.0",
                )
                Screen.Logs -> LogsScreen(
                    logText = logText,
                    onClear = vm::clearLog,
                    onRefresh = vm::refreshLog,
                )
            }
        }
    }
}
