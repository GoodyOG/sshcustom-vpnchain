package com.sshcustom.vpnchain.domain

import kotlinx.serialization.Serializable

/** Runtime state snapshot from the daemon HTTP API. */
@Serializable
data class DaemonStatus(
    val connected: Boolean = false,
    val uptimeSeconds: Long = 0L,
    val sshMode: String = "direct",
    val networkMode: String = "redirect",
    val bytesSent: Long = 0L,
    val bytesRecv: Long = 0L,
    val channelPoolSize: Int = 8,
    val channelPoolAvail: Int = 0,
    val activeConnections: Int = 0,
    val version: String = "",
    val memRssMb: Double = 0.0,
    val cpuPercent: Double = 0.0,
    val upKbps: Double = 0.0,
    val downKbps: Double = 0.0,
    val lastError: String = "",
)

/** SSH connection profile — persisted to SharedPreferences as JSON. */
@Serializable
data class Profile(
    val id: String,
    val name: String,
    val host: String,
    val port: Int = 22,
    val user: String,
    val password: String,
    val mode: String = "direct",         // direct | sni | sni_http_proxy
    val sniHost: String = "",
    val proxyHost: String = "",
    val proxyPort: Int = 3128,
    val payloadEnabled: Boolean = false,
    val payload: String = "",
)

/** App-side settings (mirrors settings.ini non-SSH options). */
@Serializable
data class AppSettings(
    val networkMode: String = "redirect",
    val socksPort: Int = 1081,      // changed from 1080 to avoid box module conflict
    val tproxyPort: Int = 9899,     // changed from 9898 to avoid box module conflict
    val redirPort: Int = 9799,      // changed from 9797 to avoid box module conflict
    val quic: String = "disable",
    val proxyTcp: Boolean = true,
    val proxyUdp: Boolean = false,
    val dnsHijackTcp: Boolean = false,
    val dnsHijackUdp: Boolean = false,
    val dnsHijackMode: String = "disable",
    val channelPool: Boolean = true,
    val channelPoolSize: Int = 8,
    val tcpBufferTuning: Boolean = true,
    val ipv6: Boolean = false,
    // Boot behaviour — default: daemon starts idle at boot, tunnel is manual
    val autostartTunnel: Boolean = false,
    // Hotspot sharing — share tunnel with hotspot clients
    val hotspotSharing: Boolean = false,
)

/** Tunnel lifecycle state — all states used in ViewModel. */
sealed class TunnelState {
    object Stopped  : TunnelState()
    object Starting : TunnelState()
    object Connected: TunnelState()
    object Stopping : TunnelState()
    data class Error(val message: String) : TunnelState()
}

data class NetSpeed(val upKbs: Float = 0f, val downKbs: Float = 0f)

/** VPN Chain (OpenVPN over SSHCustom) lifecycle state. */
sealed class VpnChainState {
    object Stopped    : VpnChainState()
    object Connecting : VpnChainState()
    object Connected  : VpnChainState()
    data class Error(val message: String) : VpnChainState()
}
