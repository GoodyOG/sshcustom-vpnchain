package com.sshcustom.vpnchain

import android.app.Application
import com.topjohnwu.superuser.Shell

class SSHCustomApp : Application() {

    override fun onCreate() {
        super.onCreate()
        // Configure libsu before any shell operations.
        // FLAG_MOUNT_MASTER ensures we get a proper root shell on KSU.
        Shell.enableVerboseLogging = false
        Shell.setDefaultBuilder(
            Shell.Builder.create()
                .setFlags(Shell.FLAG_MOUNT_MASTER)
                .setTimeout(15)
        )
    }
}
