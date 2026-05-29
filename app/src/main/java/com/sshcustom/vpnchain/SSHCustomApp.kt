package com.sshcustom.vpnchain

import android.app.Application
import com.topjohnwu.superuser.Shell

class SSHCustomApp : Application() {

    override fun onCreate() {
        super.onCreate()
        configureShell()
    }

    private fun configureShell() {
        val builder = Shell.Builder.create()
            .setTimeout(10)
        Shell.setDefaultBuilder(builder)
    }
}
