package com.sshcustom.vpnchain.domain

import kotlinx.serialization.Serializable

/** Runtime state snapshot from the daemon. */
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
)

/** SSH connection profile. */
@Serializable
data class Profile(
    val id: String,
    val name: String,
    val host: String,
    val port: Int = 22,
    val user: String,
    val password: String,
    val mode: String = "direct",   // direct | sni | sni_http_proxy
    val sniHost: String = "",
    val proxyHost: String = "",
    val proxyPort: Int = 3128,
    val payloadEnabled: Boolean = false,
    val payload: String = "",
)

/** App-side settings (mirrors settings.ini for non-SSH options). */
@Serializable
data class AppSettings(
    val networkMode: String = "redirect",
    val socksPort: Int = 1080,
    val tproxyPort: Int = 9898,
    val redirPort: Int = 9797,
    val quic: String = "disable",
    val proxyTcp: Boolean = true,
    val proxyUdp: Boolean = false,
    val dnsHijackTcp: Boolean = true,
    val dnsHijackUdp: Boolean = false,
    val dnsHijackMode: String = "redirect",
    val channelPool: Boolean = true,
    val channelPoolSize: Int = 8,
    val bbrEnabled: Boolean = true,
    val tcpBufferTuning: Boolean = true,
    val ipv6: Boolean = false,
)

sealed class TunnelState {
    object Stopped : TunnelState()
    object Starting : TunnelState()
    object Connected : TunnelState()
    object Stopping : TunnelState()
    data class Error(val message: String) : TunnelState()
}

data class NetSpeed(val upKbs: Float = 0f, val downKbs: Float = 0f)
