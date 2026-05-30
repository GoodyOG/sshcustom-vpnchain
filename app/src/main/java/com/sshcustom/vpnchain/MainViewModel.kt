package com.sshcustom.vpnchain

import android.app.Application
import android.content.ComponentName
import android.content.Intent
import android.content.ServiceConnection
import android.os.IBinder
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.sshcustom.vpnchain.data.DaemonRepository
import com.sshcustom.vpnchain.domain.*
import com.sshcustom.vpnchain.service.SSHControlService
import com.topjohnwu.superuser.Shell
import com.topjohnwu.superuser.ipc.RootService
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*

class MainViewModel(app: Application) : AndroidViewModel(app) {

    private val repo = DaemonRepository(app)

    // ── Root check ────────────────────────────────────────────────────────────
    private val _hasRoot = MutableStateFlow(false)
    val hasRoot: StateFlow<Boolean> = _hasRoot

    // ── RootService binding ───────────────────────────────────────────────────
    private var rootBinder: SSHControlService.LocalBinder? = null
    private var rootServiceBound = false

    private val serviceConn = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName?, service: IBinder?) {
            rootBinder = service as? SSHControlService.LocalBinder
            rootServiceBound = true
        }
        override fun onServiceDisconnected(name: ComponentName?) {
            rootBinder = null
            rootServiceBound = false
        }
    }

    // ── Daemon status — polled every 1s, suspended when no UI collectors ──────
    val status: StateFlow<DaemonStatus> = repo.statusFlow()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), DaemonStatus())

    // ── Tunnel state — derived + overridden during transitions ────────────────
    private val _tunnelStateOverride = MutableStateFlow<TunnelState?>(null)

    val tunnelState: StateFlow<TunnelState> = combine(
        status,
        _tunnelStateOverride,
    ) { s, override ->
        when {
            override is TunnelState.Starting -> TunnelState.Starting
            override is TunnelState.Stopping -> TunnelState.Stopping
            s.connected -> TunnelState.Connected
            s.lastError.isNotEmpty() && !s.connected -> TunnelState.Error(s.lastError)
            else -> TunnelState.Stopped
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), TunnelState.Stopped)

    // ── Loading state (shown during start/stop/restart) ───────────────────────
    private val _isLoading = MutableStateFlow(false)
    val isLoading: StateFlow<Boolean> = _isLoading

    // ── Net speed ─────────────────────────────────────────────────────────────
    val netSpeed: StateFlow<NetSpeed> = repo.netSpeedFlow()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), NetSpeed())

    // ── WAN IP ────────────────────────────────────────────────────────────────
    val wanIp: StateFlow<String> = repo.wanIpFlow()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), "—")

    // ── Logs ──────────────────────────────────────────────────────────────────
    private val _logText = MutableStateFlow("")
    val logText: StateFlow<String> = _logText

    private var logPollingJob: Job? = null

    // ── Profiles — persisted to SharedPreferences ─────────────────────────────
    private val _profiles = MutableStateFlow<List<Profile>>(emptyList())
    val profiles: StateFlow<List<Profile>> = _profiles

    private val _activeProfileId = MutableStateFlow("")
    val activeProfileId: String get() = _activeProfileId.value

    // ── Settings — persisted to SharedPreferences ─────────────────────────────
    private val _settings = MutableStateFlow(AppSettings())
    val settings: StateFlow<AppSettings> = _settings

    // Tracks whether settings were changed without restarting the tunnel
    var settingsNeedRestart = false
        private set

    // ── Init ──────────────────────────────────────────────────────────────────
    init {
        checkRoot()
        loadPersistedData()
        bindRootService()
        startLogPolling()
    }

    private fun checkRoot() = viewModelScope.launch(Dispatchers.IO) {
        val r = Shell.cmd("id").exec()
        val hasIt = r.isSuccess && (r.out.firstOrNull()?.contains("uid=0") == true)
        _hasRoot.value = hasIt
    }

    private fun loadPersistedData() {
        _profiles.value       = repo.loadProfiles()
        _activeProfileId.value = repo.loadActiveProfileId()
        _settings.value       = repo.loadSettings()
    }

    private fun bindRootService() {
        try {
            val intent = Intent(getApplication(), SSHControlService::class.java)
            RootService.bind(intent, serviceConn)
        } catch (e: Exception) {
            // Root not available — app will still work in read-only mode
        }
    }

    private fun startLogPolling() {
        logPollingJob = viewModelScope.launch {
            while (isActive) {
                refreshLogInternal()
                delay(2_000)
            }
        }
    }

    private suspend fun refreshLogInternal() {
        val binder = rootBinder ?: return
        withContext(Dispatchers.IO) {
            try {
                val log = binder.readLog(300)
                _logText.value = log
            } catch (_: Exception) {}
        }
    }

    // ── Tunnel control — use RootService binder when available ────────────────

    fun startTunnel() = viewModelScope.launch {
        _isLoading.value = true
        _tunnelStateOverride.value = TunnelState.Starting
        withContext(Dispatchers.IO) {
            try {
                val binder = rootBinder
                if (binder != null) {
                    binder.startTunnel()
                } else {
                    Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service start").exec()
                }
            } catch (_: Exception) {}
        }
        // Clear override after 8s — by then status polling will reflect reality
        delay(8_000)
        _tunnelStateOverride.value = null
        _isLoading.value = false
        settingsNeedRestart = false
    }

    fun stopTunnel() = viewModelScope.launch {
        _isLoading.value = true
        _tunnelStateOverride.value = TunnelState.Stopping
        withContext(Dispatchers.IO) {
            try {
                val binder = rootBinder
                if (binder != null) {
                    binder.stopTunnel()
                } else {
                    Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service stop").exec()
                }
            } catch (_: Exception) {}
        }
        delay(5_000)
        _tunnelStateOverride.value = null
        _isLoading.value = false
    }

    fun restartTunnel() = viewModelScope.launch {
        _isLoading.value = true
        _tunnelStateOverride.value = TunnelState.Stopping
        withContext(Dispatchers.IO) {
            try {
                val binder = rootBinder
                if (binder != null) {
                    binder.restartTunnel()
                } else {
                    Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service restart").exec()
                }
            } catch (_: Exception) {}
        }
        delay(2_000)
        _tunnelStateOverride.value = TunnelState.Starting
        delay(8_000)
        _tunnelStateOverride.value = null
        _isLoading.value = false
        settingsNeedRestart = false
    }

    fun reloadConfig() = restartTunnel()

    fun forceCleanup() = viewModelScope.launch(Dispatchers.IO) {
        try {
            rootBinder?.forceCleanup()
                ?: Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.iptables disable").exec()
        } catch (_: Exception) {}
    }

    // ── Logs ──────────────────────────────────────────────────────────────────

    fun clearLog() = viewModelScope.launch(Dispatchers.IO) {
        try {
            rootBinder?.clearLog()
                ?: Shell.cmd(": > /data/adb/sshcustom/run/sshcustom.log").exec()
            _logText.value = ""
        } catch (_: Exception) {}
    }

    fun refreshLog() = viewModelScope.launch { refreshLogInternal() }

    // ── Profiles — full CRUD with persistence ─────────────────────────────────

    fun saveProfile(profile: Profile) {
        val current = _profiles.value.toMutableList()
        val idx = current.indexOfFirst { it.id == profile.id }
        if (idx >= 0) current[idx] = profile else current.add(profile)
        _profiles.value = current
        repo.saveProfiles(current)
        // If this is the active profile, re-apply to settings.ini
        if (profile.id == _activeProfileId.value) {
            applyProfileToSettings(profile)
        }
    }

    fun selectProfile(id: String) {
        _activeProfileId.value = id
        repo.saveActiveProfileId(id)
        val profile = _profiles.value.find { it.id == id } ?: return
        applyProfileToSettings(profile)
    }

    fun deleteProfile(id: String) {
        val updated = _profiles.value.filter { it.id != id }
        _profiles.value = updated
        repo.saveProfiles(updated)
        if (_activeProfileId.value == id) {
            _activeProfileId.value = ""
            repo.saveActiveProfileId("")
        }
    }

    /**
     * Write profile SSH fields to settings.ini via the RootService binder.
     * Values are passed through SSHControlService.writeSettings() which
     * shell-escapes each value before interpolating into the sed command,
     * preventing shell injection.
     */
    private fun applyProfileToSettings(profile: Profile) = viewModelScope.launch(Dispatchers.IO) {
        try {
            val pairs = mapOf(
                "ssh_host"     to profile.host,
                "ssh_port"     to profile.port.toString(),
                "ssh_user"     to profile.user,
                "ssh_password" to profile.password,
                "ssh_mode"     to profile.mode,
                "ssh_sni_host" to profile.sniHost,
                "http_proxy_host" to profile.proxyHost,
                "http_proxy_port" to profile.proxyPort.toString(),
                "payload_enabled" to profile.payloadEnabled.toString(),
                "payload"      to profile.payload,
            )
            rootBinder?.writeSettings(pairs)
                ?: fallbackWriteSettings(pairs)
        } catch (_: Exception) {}
    }

    /** Fallback when RootService isn't bound — use raw Shell.cmd with escaping. */
    private fun fallbackWriteSettings(pairs: Map<String, String>) {
        val settingsPath = "/data/adb/sshcustom/settings.ini"
        pairs.forEach { (k, v) ->
            val safe = v
                .replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("`", "\\`")
                .replace("\$", "\\\$")
                .replace("|", "\\|")
            Shell.cmd("sed -i 's|^${k}=.*|${k}=\"${safe}\"|' $settingsPath").exec()
        }
    }

    // ── Settings ──────────────────────────────────────────────────────────────

    fun updateSettings(newSettings: AppSettings) {
        if (_settings.value != newSettings) {
            settingsNeedRestart = true
        }
        _settings.value = newSettings
        repo.saveSettings(newSettings)
        // Write non-SSH settings to settings.ini
        applySettingsToIni(newSettings)
    }

    private fun applySettingsToIni(s: AppSettings) = viewModelScope.launch(Dispatchers.IO) {
        try {
            val pairs = mapOf(
                "network_mode"     to s.networkMode,
                "socks_port"       to s.socksPort.toString(),
                "tproxy_port"      to s.tproxyPort.toString(),
                "redir_port"       to s.redirPort.toString(),
                "quic"             to s.quic,
                "proxy_tcp"        to s.proxyTcp.toString(),
                "proxy_udp"        to s.proxyUdp.toString(),
                "dns_hijack_tcp"   to s.dnsHijackTcp.toString(),
                "dns_hijack_udp"   to s.dnsHijackUdp.toString(),
                "dns_hijack_mode"  to s.dnsHijackMode,
                "channel_pool"     to s.channelPool.toString(),
                "channel_pool_size" to s.channelPoolSize.toString(),
                "bbr_enabled"      to s.bbrEnabled.toString(),
                "tcp_buffer_tuning" to s.tcpBufferTuning.toString(),
                "ipv6"             to s.ipv6.toString(),
            )
            rootBinder?.writeSettings(pairs)
                ?: fallbackWriteSettings(pairs)
        } catch (_: Exception) {}
    }

    // ── Cleanup ───────────────────────────────────────────────────────────────
    override fun onCleared() {
        super.onCleared()
        if (rootServiceBound) {
            try { RootService.unbind(serviceConn) } catch (_: Exception) {}
        }
    }
}
