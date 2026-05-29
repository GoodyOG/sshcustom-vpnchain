package com.sshcustom.vpnchain.service

import android.content.Intent
import android.os.IBinder
import com.topjohnwu.superuser.Shell
import com.topjohnwu.superuser.ipc.RootService

/**
 * RootService running in a root process via libsu.
 * Requires the libsu :service artifact.
 */
class SSHControlService : RootService() {

    override fun onBind(intent: Intent): IBinder {
        return LocalBinder()
    }

    inner class LocalBinder : android.os.Binder() {

        private val serviceScript = "/data/adb/sshcustom/scripts/ssh.service"

        fun startTunnel(): String = shell("sh $serviceScript start")
        fun stopTunnel(): String = shell("sh $serviceScript stop")
        fun restartTunnel(): String = shell("sh $serviceScript restart")

        fun isRunning(): Boolean {
            val result = Shell.cmd("sh $serviceScript status").exec()
            return result.isSuccess
        }

        fun readLog(lines: Int = 200): String {
            val result = Shell.cmd("tail -n $lines /data/adb/sshcustom/run/sshcustom.log 2>/dev/null").exec()
            return result.out.joinToString("\n")
        }

        fun clearLog() {
            Shell.cmd(": > /data/adb/sshcustom/run/sshcustom.log").exec()
        }

        fun writeSetting(key: String, value: String): Boolean {
            val escaped = value.replace("/", "\\/")
            val result = Shell.cmd(
                "sed -i 's|^${key}=.*|${key}=\"${escaped}\"|' /data/adb/sshcustom/settings.ini"
            ).exec()
            return result.isSuccess
        }

        fun forceCleanup(): String =
            shell("sh /data/adb/sshcustom/scripts/ssh.iptables disable")

        fun setAutostart(enabled: Boolean): Boolean {
            val marker = "/data/adb/sshcustom/run/autostart"
            val result = if (enabled) Shell.cmd("touch $marker").exec()
            else Shell.cmd("rm -f $marker").exec()
            return result.isSuccess
        }

        private fun shell(cmd: String): String {
            val result = Shell.cmd(cmd).exec()
            return (result.out + result.err).joinToString("\n")
        }
    }
}
