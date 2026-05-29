package com.sshcustom.vpnchain.service

import android.content.Intent
import com.topjohnwu.superuser.Shell
import com.topjohnwu.superuser.ipc.RootService

/**
 * RootService that runs in a root process via libsu.
 * All privileged operations (start/stop/restart, read files, write settings)
 * are executed here. The app process binds to this service.
 */
class SSHControlService : RootService() {

    override fun onBind(intent: Intent) = Binder()

    inner class Binder : android.os.Binder() {

        private val serviceScript = "/data/adb/sshcustom/scripts/ssh.service"
        private val settingsPath = "/data/adb/sshcustom/settings.ini"

        fun startTunnel(): String = shell("sh $serviceScript start")
        fun stopTunnel(): String = shell("sh $serviceScript stop")
        fun restartTunnel(): String = shell("sh $serviceScript restart")
        fun reloadConfig(): String = shell("sh $serviceScript restart")

        fun isRunning(): Boolean {
            val result = Shell.cmd("sh $serviceScript status").exec()
            return result.isSuccess
        }

        fun readLog(lines: Int = 200): String {
            val logFile = "/data/adb/sshcustom/run/sshcustom.log"
            val result = Shell.cmd("tail -n $lines $logFile 2>/dev/null").exec()
            return result.out.joinToString("\n")
        }

        fun clearLog() {
            Shell.cmd(": > /data/adb/sshcustom/run/sshcustom.log").exec()
        }

        fun readSettings(): String {
            val result = Shell.cmd("cat $settingsPath 2>/dev/null").exec()
            return result.out.joinToString("\n")
        }

        fun writeSetting(key: String, value: String): Boolean {
            // Escape value for sed
            val escaped = value.replace("/", "\\/").replace("&", "\\&")
            val result = Shell.cmd(
                "sed -i 's|^${key}=.*|${key}=\"${escaped}\"|' $settingsPath"
            ).exec()
            return result.isSuccess
        }

        fun forceCleanup(): String =
            shell("sh /data/adb/sshcustom/scripts/ssh.iptables disable")

        fun setAutostart(enabled: Boolean): Boolean {
            val marker = "/data/adb/sshcustom/run/autostart"
            val result = if (enabled) {
                Shell.cmd("touch $marker").exec()
            } else {
                Shell.cmd("rm -f $marker").exec()
            }
            return result.isSuccess
        }

        fun getAutostart(): Boolean {
            val result = Shell.cmd("[ -f /data/adb/sshcustom/run/autostart ] && echo 1 || echo 0").exec()
            return result.out.firstOrNull()?.trim() == "1"
        }

        private fun shell(cmd: String): String {
            val result = Shell.cmd(cmd).exec()
            return (result.out + result.err).joinToString("\n")
        }
    }
}
