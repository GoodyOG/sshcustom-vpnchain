package com.sshcustom.vpnchain

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.sshcustom.vpnchain.data.DaemonRepository
import com.sshcustom.vpnchain.domain.*
import com.topjohnwu.superuser.Shell
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*

class MainViewModel(app: Application) : AndroidViewModel(app) {

    private val repo = DaemonRepository(app)

    // Root check
    private val _hasRoot = MutableStateFlow(false)
    val hasRoot: StateFlow<Boolean> = _hasRoot

    // Daemon status (polled every 1s)
    val status: StateFlow<DaemonStatus> = repo.statusFlow()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), DaemonStatus())

    // Tunnel state derived from status
    val tunnelState: StateFlow<TunnelState> = status.map { s ->
        if (s.connected) TunnelState.Connected else TunnelState.Stopped
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), TunnelState.Stopped)

    val netSpeed: StateFlow<NetSpeed> = repo.netSpeedFlow()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), NetSpeed())

    val wanIp: StateFlow<String> = repo.wanIpFlow()
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5000), "—")

    private val _logText = MutableStateFlow("")
    val logText: StateFlow<String> = _logText

    private val _profiles = MutableStateFlow<List<Profile>>(emptyList())
    val profiles: StateFlow<List<Profile>> = _profiles

    var activeProfileId: String = ""
        private set

    private val _settings = MutableStateFlow(AppSettings())
    val settings: StateFlow<AppSettings> = _settings

    init {
        checkRoot()
        startLogPolling()
    }

    private fun checkRoot() = viewModelScope.launch(Dispatchers.IO) {
        val result = Shell.cmd("id").exec()
        _hasRoot.value = result.isSuccess && result.out.firstOrNull()?.contains("uid=0") == true
    }

    private fun startLogPolling() = viewModelScope.launch {
        repo.logFlow().collect { _logText.value = it }
    }

    // ── Tunnel control ─────────────────────────────────────────────────────
    fun startTunnel() = viewModelScope.launch(Dispatchers.IO) {
        Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service start").exec()
    }

    fun stopTunnel() = viewModelScope.launch(Dispatchers.IO) {
        Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service stop").exec()
    }

    fun restartTunnel() = viewModelScope.launch(Dispatchers.IO) {
        Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service restart").exec()
    }

    fun reloadConfig() = viewModelScope.launch(Dispatchers.IO) {
        Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.service restart").exec()
    }

    fun forceCleanup() = viewModelScope.launch(Dispatchers.IO) {
        Shell.cmd("sh /data/adb/sshcustom/scripts/ssh.iptables disable").exec()
    }

    // ── Log ────────────────────────────────────────────────────────────────
    fun clearLog() = viewModelScope.launch(Dispatchers.IO) {
        Shell.cmd(": > /data/adb/sshcustom/run/sshcustom.log").exec()
        _logText.value = ""
    }

    fun refreshLog() = viewModelScope.launch(Dispatchers.IO) {
        val result = Shell.cmd("cat /data/adb/sshcustom/run/sshcustom.log 2>/dev/null").exec()
        _logText.value = result.out.joinToString("\n")
    }

    // ── Profiles ───────────────────────────────────────────────────────────
    fun saveProfile(profile: Profile) {
        val current = _profiles.value.toMutableList()
        val idx = current.indexOfFirst { it.id == profile.id }
        if (idx >= 0) current[idx] = profile else current.add(profile)
        _profiles.value = current
        // Persist to settings via shell
        applyProfileToSettings(profile)
    }

    fun selectProfile(id: String) {
        activeProfileId = id
        val profile = _profiles.value.find { it.id == id } ?: return
        applyProfileToSettings(profile)
    }

    fun deleteProfile(id: String) {
        _profiles.value = _profiles.value.filter { it.id != id }
    }

    private fun applyProfileToSettings(profile: Profile) = viewModelScope.launch(Dispatchers.IO) {
        val s = "/data/adb/sshcustom/settings.ini"
        Shell.cmd(
            "sed -i 's|^ssh_host=.*|ssh_host=\"${profile.host}\"|' $s",
            "sed -i 's|^ssh_port=.*|ssh_port=\"${profile.port}\"|' $s",
            "sed -i 's|^ssh_user=.*|ssh_user=\"${profile.user}\"|' $s",
            "sed -i 's|^ssh_password=.*|ssh_password=\"${profile.password}\"|' $s",
            "sed -i 's|^ssh_mode=.*|ssh_mode=\"${profile.mode}\"|' $s",
        ).exec()
    }

    // ── Settings ───────────────────────────────────────────────────────────
    fun updateSettings(newSettings: AppSettings) {
        _settings.value = newSettings
        applySettingsToIni(newSettings)
    }

    private fun applySettingsToIni(s: AppSettings) = viewModelScope.launch(Dispatchers.IO) {
        val ini = "/data/adb/sshcustom/settings.ini"
        Shell.cmd(
            "sed -i 's|^network_mode=.*|network_mode=\"${s.networkMode}\"|' $ini",
            "sed -i 's|^quic=.*|quic=\"${s.quic}\"|' $ini",
            "sed -i 's|^proxy_tcp=.*|proxy_tcp=\"${s.proxyTcp}\"|' $ini",
            "sed -i 's|^proxy_udp=.*|proxy_udp=\"${s.proxyUdp}\"|' $ini",
            "sed -i 's|^dns_hijack_mode=.*|dns_hijack_mode=\"${s.dnsHijackMode}\"|' $ini",
            "sed -i 's|^channel_pool=.*|channel_pool=\"${s.channelPool}\"|' $ini",
            "sed -i 's|^bbr_enabled=.*|bbr_enabled=\"${s.bbrEnabled}\"|' $ini",
        ).exec()
    }
}
