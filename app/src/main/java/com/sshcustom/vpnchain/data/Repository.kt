package com.sshcustom.vpnchain.data

import android.content.Context
import com.sshcustom.vpnchain.domain.DaemonStatus
import com.sshcustom.vpnchain.domain.NetSpeed
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.long
import kotlinx.serialization.json.double
import kotlinx.serialization.json.int
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * Repository for daemon data. Polls the HTTP API every second.
 * Falls back gracefully if daemon is not running.
 */
class DaemonRepository(private val context: Context) {

    private val http = OkHttpClient.Builder()
        .connectTimeout(1, TimeUnit.SECONDS)
        .readTimeout(1, TimeUnit.SECONDS)
        .build()

    private val baseUrl = "http://127.0.0.1:9190"
    private val json = Json { ignoreUnknownKeys = true }

    fun statusFlow(): Flow<DaemonStatus> = flow {
        while (true) {
            emit(fetchStatus())
            delay(1000)
        }
    }.flowOn(Dispatchers.IO)

    fun netSpeedFlow(): Flow<NetSpeed> = flow {
        var prevRx = readNetBytes("rx")
        var prevTx = readNetBytes("tx")
        var prevTime = System.currentTimeMillis()
        while (true) {
            delay(1000)
            val rx = readNetBytes("rx")
            val tx = readNetBytes("tx")
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

    fun wanIpFlow(): Flow<String> = flow {
        while (true) {
            emit(fetchWanIp())
            delay(60_000)
        }
    }.flowOn(Dispatchers.IO)

    fun logFlow(): Flow<String> = flow {
        while (true) {
            emit(readLog())
            delay(2000)
        }
    }.flowOn(Dispatchers.IO)

    private fun fetchStatus(): DaemonStatus {
        return try {
            val req = Request.Builder().url("$baseUrl/api/v1/status").build()
            val body = http.newCall(req).execute().body?.string() ?: return DaemonStatus()
            val root = json.parseToJsonElement(body).jsonObject
            val data = root["data"]?.jsonObject ?: return DaemonStatus()
            val runtime = data["runtime"]?.jsonObject ?: return DaemonStatus()
            DaemonStatus(
                connected       = runtime["connected"]?.jsonPrimitive?.boolean ?: false,
                uptimeSeconds   = runtime["uptime_seconds"]?.jsonPrimitive?.long ?: 0,
                sshMode         = runtime["ssh_mode"]?.jsonPrimitive?.content ?: "direct",
                networkMode     = runtime["network_mode"]?.jsonPrimitive?.content ?: "redirect",
                channelPoolSize = runtime["channel_pool_size"]?.jsonPrimitive?.int ?: 8,
                channelPoolAvail= runtime["channel_pool_available"]?.jsonPrimitive?.int ?: 0,
                version         = runtime["version"]?.jsonPrimitive?.content ?: "",
                memRssMb        = runtime["mem_rss_mb"]?.jsonPrimitive?.double ?: 0.0,
                cpuPercent      = runtime["cpu_percent"]?.jsonPrimitive?.double ?: 0.0,
            )
        } catch (e: Exception) {
            DaemonStatus()
        }
    }

    private fun fetchWanIp(): String {
        return try {
            val req = Request.Builder()
                .url("https://ip-api.com/json?fields=query,country,isp")
                .build()
            val body = http.newCall(req).execute().body?.string() ?: return "—"
            val obj = json.parseToJsonElement(body).jsonObject
            val ip = obj["query"]?.jsonPrimitive?.content ?: ""
            val country = obj["country"]?.jsonPrimitive?.content ?: ""
            "$ip  $country"
        } catch (e: Exception) {
            "—"
        }
    }

    private fun readLog(): String {
        return try {
            val file = File("/data/adb/sshcustom/run/sshcustom.log")
            if (file.exists()) file.readText() else "(log not found)"
        } catch (e: Exception) {
            "(cannot read log)"
        }
    }

    private fun readNetBytes(direction: String): Long {
        // Read from /proc/net/dev — sum all non-loopback interfaces
        return try {
            File("/proc/net/dev").readLines()
                .drop(2) // skip headers
                .filterNot { it.trimStart().startsWith("lo:") }
                .sumOf { line ->
                    val parts = line.trim().split(Regex("\\s+"))
                    when (direction) {
                        "rx" -> parts.getOrNull(1)?.toLongOrNull() ?: 0L
                        "tx" -> parts.getOrNull(9)?.toLongOrNull() ?: 0L
                        else -> 0L
                    }
                }
        } catch (e: Exception) { 0L }
    }
}
