package com.sshcustom.vpnchain.service

import android.content.Intent
import android.os.IBinder
import com.topjohnwu.superuser.Shell
import com.topjohnwu.superuser.ipc.RootService

/**
 * RootService that runs in a privileged ":root" process via libsu.
 * The app process binds to this service and calls methods on [LocalBinder].
 *
 * All shell commands go through this service so there is ONE root shell
 * connection rather than spawning a new shell per command.
 */
class SSHControlService : RootService() {

    override fun onBind(intent: Intent): IBinder = LocalBinder()

    inner class LocalBinder : android.os.Binder() {

        private val settingsPath = "/data/adb/sshcustom/settings.ini"
        private val serviceScript = "/data/adb/sshcustom/scripts/ssh.service"
        private val iptablesScript = "/data/adb/sshcustom/scripts/ssh.iptables"
        private val logFile = "/data/adb/sshcustom/run/sshcustom.log"
        private val ovpnService = "/data/adb/sshcustom/scripts/ovpn.service"
        private val ovpnLog = "/data/adb/sshcustom/run/openvpn.log"
        private val vpnConfigDir = "/data/adb/sshcustom/vpnchain/configs"

        // ── VPN Chain (OpenVPN over SSHCustom) ────────────────────────────────
        /** List available .ovpn config filenames in the configs directory. */
        fun listVpnConfigs(): List<String> {
            val r = Shell.cmd("ls -1 '$vpnConfigDir' 2>/dev/null").exec()
            return r.out.map { it.trim() }.filter { it.endsWith(".ovpn", ignoreCase = true) }
        }

        fun startVpnChain(config: String): String {
            val safe = config.replace("'", "'\\''")
            return shell("sh $ovpnService start '$safe' &")
        }

        fun stopVpnChain(): String = shell("sh $ovpnService stop")

        /** Returns "connected" | "connecting" | "stopped". */
        fun vpnChainStatus(): String {
            val r = Shell.cmd("sh $ovpnService status").exec()
            return r.out.firstOrNull()?.trim() ?: "stopped"
        }

        fun readOvpnLog(lines: Int = 200): String = readLogFile(ovpnLog, lines)

        // ── Tunnel control ────────────────────────────────────────────────────
        fun startTunnel(): String  = shell("sh $serviceScript start")
        fun stopTunnel(): String   = shell("sh $serviceScript stop")
        fun restartTunnel(): String = shell("sh $serviceScript restart")
        fun startIdle(): String    = shell("sh $serviceScript start-idle")

        fun isRunning(): Boolean {
            val result = Shell.cmd("sh $serviceScript status").exec()
            return result.isSuccess
        }

        // ── iptables cleanup ──────────────────────────────────────────────────
        fun forceCleanup(): String = shell("sh $iptablesScript disable")

        // ── Autostart ─────────────────────────────────────────────────────────
        fun setAutostart(enabled: Boolean): Boolean {
            val marker = "/data/adb/sshcustom/run/autostart"
            val cmd = if (enabled) "touch $marker" else "rm -f $marker"
            return Shell.cmd(cmd).exec().isSuccess
        }

        fun getAutostart(): Boolean {
            val r = Shell.cmd("[ -f /data/adb/sshcustom/run/autostart ] && echo 1 || echo 0").exec()
            return r.out.firstOrNull()?.trim() == "1"
        }

        // ── Logs ──────────────────────────────────────────────────────────────
        /** Read the last N lines of any log file by path. */
        fun readLogFile(path: String, lines: Int = 300): String {
            val r = Shell.cmd("tail -n $lines '$path' 2>/dev/null || echo '(log empty)'").exec()
            return (r.out + r.err).joinToString("\n")
        }

        /** Truncate any log file by path. */
        fun clearLogFile(path: String) {
            Shell.cmd(": > '$path'").exec()
        }

        /** Read the last N lines of the daemon log. Root required because log is 0600. */
        fun readLog(lines: Int = 300): String = readLogFile(logFile, lines)

        fun clearLog() = clearLogFile(logFile)

        // ── settings.ini write ────────────────────────────────────────────────
        /**
         * Write a single key=value to settings.ini.
         * Value is shell-escaped to prevent injection: replaces | with pipe-safe form.
         */
        fun writeSetting(key: String, value: String): Boolean {
            val safe = value
                .replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("`", "\\`")
                .replace("$", "\\$")
                .replace("|", "\\|")
            val cmd = "sed -i 's|^${key}=.*|${key}=\"${safe}\"|' $settingsPath"
            return Shell.cmd(cmd).exec().isSuccess
        }

        /** Write multiple settings atomically using a single sed invocation. */
        fun writeSettings(pairs: Map<String, String>): Boolean {
            if (pairs.isEmpty()) return true
            val script = buildString {
                appendLine("#!/system/bin/sh")
                pairs.forEach { (k, v) ->
                    val safe = v
                        .replace("\\", "\\\\")
                        .replace("\"", "\\\"")
                        .replace("`", "\\`")
                        .replace("$", "\\$")
                        .replace("|", "\\|")
                    appendLine("sed -i 's|^${k}=.*|${k}=\"${safe}\"|' $settingsPath")
                }
            }
            val r = Shell.cmd(script).exec()
            return r.isSuccess
        }

        /** Read the current value of a single key from settings.ini. */
        fun readSetting(key: String): String {
            val r = Shell.cmd(
                "grep -E '^${key}=' $settingsPath | head -1 | cut -d'=' -f2- | tr -d '\"'"
            ).exec()
            return r.out.firstOrNull()?.trim() ?: ""
        }

        private fun shell(cmd: String): String {
            val r = Shell.cmd(cmd).exec()
            return (r.out + r.err).joinToString("\n")
        }
    }
}
