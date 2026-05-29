package com.sshcustom.vpnchain

import android.app.Application
import com.topjohnwu.superuser.Shell

class SSHCustomApp : Application() {
    override fun onCreate() {
        super.onCreate()
        // Configure libsu: non-interactive, 10s timeout
        Shell.enableVerboseLogging = false
        Shell.setDefaultBuilder(
            Shell.Builder.create()
                .setFlags(Shell.FLAG_REDIRECT_STDERR)
                .setTimeout(10)
        )
    }
}
