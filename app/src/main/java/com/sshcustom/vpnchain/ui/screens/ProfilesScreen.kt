package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.Profile
import top.yukonga.miuix.kmp.basic.ButtonDefaults
import top.yukonga.miuix.kmp.basic.Card
import top.yukonga.miuix.kmp.basic.SmallTitle
import top.yukonga.miuix.kmp.basic.Text
import top.yukonga.miuix.kmp.basic.TextButton
import top.yukonga.miuix.kmp.basic.TextField
import top.yukonga.miuix.kmp.extra.SuperArrow
import top.yukonga.miuix.kmp.extra.SuperSwitch
import top.yukonga.miuix.kmp.theme.MiuixTheme
import java.util.UUID

@Composable
fun ProfilesScreen(
    profiles: List<Profile>,
    activeProfileId: String,
    onSelectProfile: (String) -> Unit,
    onSaveProfile: (Profile) -> Unit,
    onDeleteProfile: (String) -> Unit,
    paddingValues: PaddingValues,
) {
    var showEditor by remember { mutableStateOf(false) }
    var editingProfile by remember { mutableStateOf<Profile?>(null) }

    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())
            .padding(paddingValues).padding(horizontal = 12.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Spacer(Modifier.height(4.dp))
        TextButton(
            text = "+ Add Profile",
            onClick = { editingProfile = null; showEditor = true },
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.textButtonColorsPrimary(),
        )
        if (profiles.isEmpty()) {
            Card(Modifier.fillMaxWidth()) {
                Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) {
                    Text("No profiles yet. Tap '+ Add Profile' to create one.",
                        color = MiuixTheme.colorScheme.onSurfaceVariantActions, fontSize = 14.sp)
                }
            }
        } else {
            profiles.forEach { p ->
                ProfileCard(
                    profile = p, isActive = p.id == activeProfileId,
                    onSelect = { onSelectProfile(p.id) },
                    onEdit = { editingProfile = p; showEditor = true },
                    onDelete = { onDeleteProfile(p.id) },
                )
            }
        }
        Spacer(Modifier.height(8.dp))
    }

    if (showEditor) {
        ProfileEditor(
            initial = editingProfile,
            onSave = { onSaveProfile(it); showEditor = false },
            onDismiss = { showEditor = false },
        )
    }
}

@Composable
private fun ProfileCard(
    profile: Profile, isActive: Boolean,
    onSelect: () -> Unit, onEdit: () -> Unit, onDelete: () -> Unit,
) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(profile.name, fontWeight = FontWeight.SemiBold, fontSize = 15.sp, color = MiuixTheme.colorScheme.onSurface)
                    Text("${profile.host}:${profile.port}", fontSize = 12.sp, color = MiuixTheme.colorScheme.onSurfaceVariantActions)
                    Text(profile.mode.uppercase(), fontSize = 11.sp, color = MiuixTheme.colorScheme.primary)
                }
                if (isActive) Text("Active", fontSize = 12.sp, color = MiuixTheme.colorScheme.primary,
                    fontWeight = FontWeight.Medium, modifier = Modifier.padding(end = 8.dp))
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (!isActive) TextButton("Select", onSelect, modifier = Modifier.weight(1f), colors = ButtonDefaults.textButtonColorsPrimary())
                TextButton("Edit", onEdit, modifier = Modifier.weight(1f))
                TextButton("Delete", onDelete, modifier = Modifier.weight(1f),
                    colors = ButtonDefaults.textButtonColors(color = MiuixTheme.colorScheme.error, textColor = Color.White,
                        disabledColor = MiuixTheme.colorScheme.disabledSecondaryVariant,
                        disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant))
            }
        }
    }
}

@Composable
private fun ProfileEditor(initial: Profile?, onSave: (Profile) -> Unit, onDismiss: () -> Unit) {
    var name     by remember { mutableStateOf(initial?.name ?: "") }
    var host     by remember { mutableStateOf(initial?.host ?: "") }
    var port     by remember { mutableStateOf(initial?.port?.toString() ?: "22") }
    var user     by remember { mutableStateOf(initial?.user ?: "") }
    var password by remember { mutableStateOf(initial?.password ?: "") }
    var mode     by remember { mutableStateOf(initial?.mode ?: "direct") }
    var sniHost  by remember { mutableStateOf(initial?.sniHost ?: "") }
    var proxyHost by remember { mutableStateOf(initial?.proxyHost ?: "") }
    var proxyPort by remember { mutableStateOf(initial?.proxyPort?.toString() ?: "3128") }
    var payloadEnabled by remember { mutableStateOf(initial?.payloadEnabled ?: false) }
    var payload  by remember { mutableStateOf(initial?.payload ?: "") }
    var showPwd  by remember { mutableStateOf(false) }
    var error    by remember { mutableStateOf("") }

    Column(
        modifier = Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(if (initial == null) "New Profile" else "Edit Profile",
            fontSize = 18.sp, fontWeight = FontWeight.SemiBold, color = MiuixTheme.colorScheme.onSurface)
        if (error.isNotEmpty()) Text(error, color = MiuixTheme.colorScheme.error, fontSize = 13.sp)

        TextField(value = name, onValueChange = { name = it; error = "" }, label = "Profile Name *", singleLine = true, modifier = Modifier.fillMaxWidth())
        TextField(value = host, onValueChange = { host = it.trim(); error = "" }, label = "Server Host *", singleLine = true, modifier = Modifier.fillMaxWidth())
        TextField(value = port, onValueChange = { port = it.filter { c -> c.isDigit() }.take(5) }, label = "Port",
            singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth())
        TextField(value = user, onValueChange = { user = it }, label = "Username", singleLine = true, modifier = Modifier.fillMaxWidth())
        TextField(value = password, onValueChange = { password = it }, label = "Password", singleLine = true,
            visualTransformation = if (showPwd) VisualTransformation.None else PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth())
        TextButton(text = if (showPwd) "Hide password" else "Show password", onClick = { showPwd = !showPwd },
            modifier = Modifier.wrapContentWidth())

        SmallTitle("SSH Mode")
        Card(Modifier.fillMaxWidth()) {
            listOf("direct" to "Direct", "sni" to "SNI", "sni_http_proxy" to "SNI + HTTP Proxy").forEach { (m, l) ->
                SuperArrow(title = l,
                    rightActions = { if (mode == m) Text("✓", color = MiuixTheme.colorScheme.primary, fontSize = MiuixTheme.textStyles.body2.fontSize) },
                    onClick = { mode = m })
            }
        }

        if (mode == "sni" || mode == "sni_http_proxy")
            TextField(value = sniHost, onValueChange = { sniHost = it }, label = "SNI Host", singleLine = true, modifier = Modifier.fillMaxWidth())
        if (mode == "sni_http_proxy") {
            TextField(value = proxyHost, onValueChange = { proxyHost = it }, label = "HTTP Proxy Host", singleLine = true, modifier = Modifier.fillMaxWidth())
            TextField(value = proxyPort, onValueChange = { proxyPort = it.filter { c -> c.isDigit() }.take(5) }, label = "HTTP Proxy Port",
                singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth())
        }

        SuperSwitch(checked = payloadEnabled, onCheckedChange = { payloadEnabled = it }, title = "Enable Payload Injection")
        if (payloadEnabled)
            TextField(value = payload, onValueChange = { payload = it },
                label = "Payload (vars: [host] [port] [crlf] [cr] [lf])",
                modifier = Modifier.fillMaxWidth(), minLines = 3)

        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            TextButton("Cancel", onDismiss, modifier = Modifier.weight(1f))
            TextButton("Save", onClick = {
                if (name.isBlank() || host.isBlank()) { error = "Name and host are required"; return@TextButton }
                val p = port.toIntOrNull()
                if (p == null || p !in 1024..65535) { error = "Port must be 1024–65535"; return@TextButton }
                onSave(Profile(id = initial?.id ?: UUID.randomUUID().toString(), name = name.trim(),
                    host = host.trim(), port = p, user = user.trim(), password = password, mode = mode,
                    sniHost = sniHost.trim(), proxyHost = proxyHost.trim(),
                    proxyPort = proxyPort.toIntOrNull() ?: 3128,
                    payloadEnabled = payloadEnabled, payload = payload))
            }, modifier = Modifier.weight(1f), colors = ButtonDefaults.textButtonColorsPrimary())
        }
        Spacer(Modifier.height(16.dp))
    }
}
