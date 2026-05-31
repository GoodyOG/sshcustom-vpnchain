package com.sshcustom.vpnchain.data

import android.content.Context
import android.content.SharedPreferences
import com.sshcustom.vpnchain.domain.AppSettings
import com.sshcustom.vpnchain.domain.DaemonStatus
import com.sshcustom.vpnchain.domain.NetSpeed
import com.sshcustom.vpnchain.domain.Profile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.double
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.util.concurrent.TimeUnit

private const val PREFS_NAME   = "sshcustom_prefs"
private const val KEY_PROFILES = "profiles_json"
private const val KEY_ACTIVE   = "active_profile_id"
private const val KEY_SETTINGS = "app_settings_json"
private const val KEY_VPN_CONFIG = "vpn_selected_config"
private const val KEY_LAST_CONN    = "last_connected"
private const val KEY_LAST_SSHMODE = "last_ssh_mode"
private const val KEY_LAST_NETMODE = "last_net_mode"
private const val KEY_LAST_VER     = "last_version"

/**
 * Repository for daemon data and local persistence.
 *
 * Persistence uses plain SharedPreferences + kotlinx.serialization JSON.
 * All daemon HTTP calls go through the loopback interface (127.0.0.1:9190).
 * Log reading goes through libsu Shell.cmd because the log file is root-owned.
 */
class DaemonRepository(private val context: Context) {

    private val prefs: SharedPreferences =
        context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    // Short timeouts — we're calling loopback, should be instant
    private val http = OkHttpClient.Builder()
        .connectTimeout(2, TimeUnit.SECONDS)
        .readTimeout(2, TimeUnit.SECONDS)
        .build()

    private val baseUrl = "http://127.0.0.1:9190"

    // Longer-timeout client for the chained exit-IP lookup (goes through the
    // full SSH+OpenVPN chain, so it can be slower than a loopback call).
    private val longHttp = OkHttpClient.Builder()
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(8, TimeUnit.SECONDS)
        .build()

    // ── Polling flows ─────────────────────────────────────────────────────────

    /** Polls daemon status every second. Stops when no collectors (WhileSubscribed). */
    fun statusFlow(): Flow<DaemonStatus> = flow {
        while (true) {
            emit(fetchStatus())
            delay(1000)
        }
    }.flowOn(Dispatchers.IO)

    /** Computes network speed every second from /proc/net/dev. */
    fun netSpeedFlow(): Flow<NetSpeed> = flow {
        var prevRx = readNetBytes(RX)
        var prevTx = readNetBytes(TX)
        var prevTime = System.currentTimeMillis()
        while (true) {
            delay(1000)
            val rx = readNetBytes(RX)
            val tx = readNetBytes(TX)
            val now = System.currentTimeMillis()
            val dt = (now - prevTime) / 1000f
            if (dt > 0) {
                val downKbs = ((rx - prevRx) / dt / 1024).coerceAtLeast(0f)
                val upKbs   = ((tx - prevTx) / dt / 1024).coerceAtLeast(0f)
                emit(NetSpeed(upKbs, downKbs))
            }
            prevRx = rx; prevTx = tx; prevTime = now
        }
    }.flowOn(Dispatchers.IO)

    /** Fetches WAN IP once, then re-fetches every 60s. */
    fun wanIpFlow(): Flow<String> = flow {
        while (true) {
            emit(fetchWanIp())
            delay(60_000)
        }
    }.flowOn(Dispatchers.IO)

    // ── Daemon HTTP calls ─────────────────────────────────────────────────────

    private fun fetchStatus(): DaemonStatus {
        return try {
            val req = Request.Builder().url("$baseUrl/api/v1/status").build()
            val body = http.newCall(req).execute().use { it.body?.string() } ?: return DaemonStatus()
            val root    = json.parseToJsonElement(body).jsonObject
            val data    = root["data"]?.jsonObject ?: return DaemonStatus()
            val runtime = data["runtime"]?.jsonObject ?: return DaemonStatus()
            DaemonStatus(
                connected        = runtime["connected"]?.jsonPrimitive?.boolean ?: false,
                uptimeSeconds    = runtime["uptime_seconds"]?.jsonPrimitive?.long ?: 0,
                sshMode          = runtime["ssh_mode"]?.jsonPrimitive?.content ?: "direct",
                networkMode      = runtime["network_mode"]?.jsonPrimitive?.content ?: "redirect",
                channelPoolSize  = runtime["channel_pool_size"]?.jsonPrimitive?.int ?: 8,
                channelPoolAvail = runtime["channel_pool_available"]?.jsonPrimitive?.int ?: 0,
                activeConnections= runtime["active_connections"]?.jsonPrimitive?.int ?: 0,
                version          = runtime["version"]?.jsonPrimitive?.content ?: "",
                memRssMb         = runtime["mem_rss_mb"]?.jsonPrimitive?.double ?: 0.0,
                cpuPercent       = runtime["cpu_percent"]?.jsonPrimitive?.double ?: 0.0,
                upKbps           = runtime["up_kbps"]?.jsonPrimitive?.double ?: 0.0,
                downKbps         = runtime["down_kbps"]?.jsonPrimitive?.double ?: 0.0,
                lastError        = runtime["last_error"]?.jsonPrimitive?.content ?: "",
            )
        } catch (_: Exception) { DaemonStatus() }
    }

    /** Fetches WAN IP via the daemon's local API (goes through the tunnel). */
    private fun fetchWanIp(): String {
        return try {
            // Use daemon's public-ip endpoint — it proxies through SOCKS5 so result is tunnel-side IP
            val req = Request.Builder().url("$baseUrl/api/v1/network/public-ip").build()
            val body = http.newCall(req).execute().use { it.body?.string() } ?: return "—"
            val root = json.parseToJsonElement(body).jsonObject
            val data = root["data"]?.jsonObject ?: return "—"
            val tunnel = data["tunnel"]?.jsonObject ?: return "—"
            val ip      = tunnel["ip"]?.jsonPrimitive?.content ?: ""
            val country = tunnel["country"]?.jsonPrimitive?.content ?: ""
            if (ip.isNotEmpty()) "$ip  $country" else "—"
        } catch (_: Exception) { "—" }
    }

    // ── Profile persistence ───────────────────────────────────────────────────

    fun loadProfiles(): List<Profile> = try {
        val raw = prefs.getString(KEY_PROFILES, null) ?: return emptyList()
        json.decodeFromString<List<Profile>>(raw)
    } catch (_: Exception) { emptyList() }

    fun saveProfiles(profiles: List<Profile>) {
        prefs.edit().putString(KEY_PROFILES, json.encodeToString(profiles)).apply()
    }

    fun loadActiveProfileId(): String =
        prefs.getString(KEY_ACTIVE, "") ?: ""

    fun saveActiveProfileId(id: String) {
        prefs.edit().putString(KEY_ACTIVE, id).apply()
    }

    // ── Settings persistence ──────────────────────────────────────────────────

    fun loadSettings(): AppSettings = try {
        val raw = prefs.getString(KEY_SETTINGS, null) ?: return AppSettings()
        json.decodeFromString<AppSettings>(raw)
    } catch (_: Exception) { AppSettings() }

    fun saveSettings(settings: AppSettings) {
        prefs.edit().putString(KEY_SETTINGS, json.encodeToString(settings)).apply()
    }

    // ── VPN Chain persistence ─────────────────────────────────────────────────

    fun loadVpnConfig(): String = prefs.getString(KEY_VPN_CONFIG, "") ?: ""
    fun saveVpnConfig(name: String) { prefs.edit().putString(KEY_VPN_CONFIG, name).apply() }

    /**
     * The exit IP as seen through the full chain. When the VPN Chain is up the
     * app's own traffic routes through OpenVPN, so this returns the Windscribe
     * IP/country. Uses HTTPS so Android's cleartext policy doesn't block it.
     */
    fun fetchChainExitIp(): String {
        return try {
            val req = Request.Builder().url("https://ipinfo.io/json").build()
            val body = longHttp.newCall(req).execute().use { it.body?.string() } ?: return "—"
            val root = json.parseToJsonElement(body).jsonObject
            val ip = root["ip"]?.jsonPrimitive?.content ?: ""
            val country = root["country"]?.jsonPrimitive?.content ?: ""
            if (ip.isNotEmpty()) (if (country.isNotEmpty()) "$ip  $country" else ip) else "—"
        } catch (_: Exception) { "—" }
    }

    // ── Last-known state (seed on cold start to avoid the "Stopped" flash) ────

    fun saveLastState(s: DaemonStatus) {
        prefs.edit()
            .putBoolean(KEY_LAST_CONN, s.connected)
            .putString(KEY_LAST_SSHMODE, s.sshMode)
            .putString(KEY_LAST_NETMODE, s.networkMode)
            .putString(KEY_LAST_VER, s.version)
            .apply()
    }

    fun loadLastState(): DaemonStatus = DaemonStatus(
        connected   = prefs.getBoolean(KEY_LAST_CONN, false),
        sshMode     = prefs.getString(KEY_LAST_SSHMODE, "direct") ?: "direct",
        networkMode = prefs.getString(KEY_LAST_NETMODE, "redirect") ?: "redirect",
        version     = prefs.getString(KEY_LAST_VER, "") ?: "",
    )

    // ── Net speed helper ──────────────────────────────────────────────────────

    private fun readNetBytes(direction: Int): Long {
        return try {
            File("/proc/net/dev").readLines().drop(2)
                .filterNot { it.trimStart().startsWith("lo:") }
                .sumOf { line ->
                    val parts = line.trim().split(Regex("\\s+"))
                    when (direction) {
                        RX -> parts.getOrNull(1)?.toLongOrNull() ?: 0L
                        TX -> parts.getOrNull(9)?.toLongOrNull() ?: 0L
                        else -> 0L
                    }
                }
        } catch (_: Exception) { 0L }
    }

    companion object {
        private const val RX = 0
        private const val TX = 1
    }
}
