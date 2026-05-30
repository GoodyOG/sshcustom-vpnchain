package com.sshcustom.vpnchain

import android.app.Application
import com.topjohnwu.superuser.Shell

class SSHCustomApp : Application() {

    override fun onCreate() {
        super.onCreate()
        configureShell()
    }

    private fun configureShell() {
        Shell.setDefaultBuilder(
            Shell.Builder.create()
                .setTimeout(15)
        )
    }
}
