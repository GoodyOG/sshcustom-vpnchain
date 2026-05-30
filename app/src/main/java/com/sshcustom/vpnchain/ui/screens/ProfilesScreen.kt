package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.Profile
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.preference.ArrowPreference
import top.yukonga.miuix.kmp.preference.SwitchPreference
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
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(paddingValues)
            .padding(horizontal = 12.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp)
    ) {
        Spacer(Modifier.height(4.dp))

        // Add profile button
        TextButton(
            text = "+ Add Profile",
            onClick = { editingProfile = null; showEditor = true },
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.textButtonColorsPrimary(),
        )

        if (profiles.isEmpty()) {
            Card(modifier = Modifier.fillMaxWidth()) {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(32.dp),
                    contentAlignment = Alignment.Center
                ) {
                    Text(
                        text = "No profiles yet.\nTap '+ Add Profile' to create one.",
                        color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                        fontSize = 14.sp,
                    )
                }
            }
        } else {
            profiles.forEach { profile ->
                ProfileCard(
                    profile = profile,
                    isActive = profile.id == activeProfileId,
                    onSelect = { onSelectProfile(profile.id) },
                    onEdit = { editingProfile = profile; showEditor = true },
                    onDelete = { onDeleteProfile(profile.id) },
                )
            }
        }

        Spacer(Modifier.height(8.dp))
    }

    if (showEditor) {
        ProfileEditorSheet(
            initial = editingProfile,
            onSave = { profile -> onSaveProfile(profile); showEditor = false },
            onDismiss = { showEditor = false },
        )
    }
}

@Composable
private fun ProfileCard(
    profile: Profile,
    isActive: Boolean,
    onSelect: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = profile.name,
                        fontWeight = FontWeight.SemiBold,
                        fontSize = 15.sp,
                        color = MiuixTheme.colorScheme.onSurface,
                    )
                    Text(
                        text = "${profile.host}:${profile.port}",
                        fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                    )
                    Text(
                        text = profile.mode.uppercase(),
                        fontSize = 11.sp,
                        color = MiuixTheme.colorScheme.primary,
                    )
                }
                if (isActive) {
                    Text(
                        text = "Active",
                        fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.primary,
                        fontWeight = FontWeight.Medium,
                        modifier = Modifier.padding(end = 8.dp),
                    )
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (!isActive) {
                    TextButton(
                        text = "Select",
                        onClick = onSelect,
                        modifier = Modifier.weight(1f),
                        colors = ButtonDefaults.textButtonColorsPrimary(),
                    )
                }
                TextButton(
                    text = "Edit",
                    onClick = onEdit,
                    modifier = Modifier.weight(1f),
                )
                TextButton(
                    text = "Delete",
                    onClick = onDelete,
                    modifier = Modifier.weight(1f),
                    colors = ButtonDefaults.textButtonColorsDanger(),
                )
            }
        }
    }
}

@Composable
private fun ProfileEditorSheet(
    initial: Profile?,
    onSave: (Profile) -> Unit,
    onDismiss: () -> Unit,
) {
    // Mutable state for every field
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
    var showPassword by remember { mutableStateOf(false) }

    // Validation errors
    var nameError by remember { mutableStateOf(false) }
    var hostError by remember { mutableStateOf(false) }

    var error by remember { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Text(
            text = if (initial == null) "New Profile" else "Edit Profile",
            fontSize = 18.sp,
            fontWeight = FontWeight.SemiBold,
            color = MiuixTheme.colorScheme.onSurface,
        )

        if (error.isNotEmpty()) {
            Text(text = error, color = MiuixTheme.colorScheme.error, fontSize = 13.sp)
        }

        TextField(
            value = name,
            onValueChange = { name = it; nameError = false; error = "" },
            label = "Profile Name *",
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        if (nameError) Text("Profile name is required", color = MiuixTheme.colorScheme.error, fontSize = 11.sp)

        TextField(
            value = host,
            onValueChange = { host = it.trim(); hostError = false; error = "" },
            label = "Server Host *",
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        if (hostError) Text("Server host is required", color = MiuixTheme.colorScheme.error, fontSize = 11.sp)

        TextField(
            value = port,
            onValueChange = { port = it.filter { c -> c.isDigit() }.take(5) },
            label = "Port (1024–65535)",
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
            modifier = Modifier.fillMaxWidth(),
        )
        TextField(
            value = user,
            onValueChange = { user = it },
            label = "Username",
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        TextField(
            value = password,
            onValueChange = { password = it },
            label = "Password",
            singleLine = true,
            visualTransformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth(),
        )
        TextButton(
            text = if (showPassword) "Hide password" else "Show password",
            onClick = { showPassword = !showPassword },
            modifier = Modifier.wrapContentWidth(),
        )

        // SSH Mode
        SmallTitle(text = "SSH Mode", modifier = Modifier.padding(0.dp))
        Card(modifier = Modifier.fillMaxWidth()) {
            listOf("direct" to "Direct", "sni" to "SNI", "sni_http_proxy" to "SNI + HTTP Proxy")
                .forEach { (m, l) ->
                    ArrowPreference(
                        title = l,
                        endActions = {
                            if (mode == m) Text("✓", color = MiuixTheme.colorScheme.primary,
                                fontSize = MiuixTheme.textStyles.body2.fontSize)
                        },
                        onClick = { mode = m },
                    )
                }
        }

        if (mode == "sni" || mode == "sni_http_proxy") {
            TextField(
                value = sniHost,
                onValueChange = { sniHost = it },
                label = "SNI Host",
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (mode == "sni_http_proxy") {
            TextField(
                value = proxyHost,
                onValueChange = { proxyHost = it },
                label = "HTTP Proxy Host",
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            TextField(
                value = proxyPort,
                onValueChange = { proxyPort = it.filter { c -> c.isDigit() }.take(5) },
                label = "HTTP Proxy Port",
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth(),
            )
        }

        // Payload
        SwitchPreference(
            title = "Enable Payload Injection",
            checked = payloadEnabled,
            onCheckedChange = { payloadEnabled = it },
        )
        if (payloadEnabled) {
            TextField(
                value = payload,
                onValueChange = { payload = it },
                label = "Payload (vars: [host] [port] [crlf] [cr] [lf])",
                modifier = Modifier.fillMaxWidth(),
                minLines = 3,
            )
        }

        // Save / Cancel
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            TextButton(text = "Cancel", onClick = onDismiss, modifier = Modifier.weight(1f))
            TextButton(
                text = "Save",
                onClick = {
                    nameError = name.isBlank()
                    hostError = host.isBlank()
                    val portVal = port.toIntOrNull()
                    if (nameError || hostError) { error = "Please fill required fields"; return@TextButton }
                    if (portVal == null || portVal !in 1024..65535) { error = "Port must be 1024–65535"; return@TextButton }
                    onSave(Profile(
                        id            = initial?.id ?: UUID.randomUUID().toString(),
                        name          = name.trim(),
                        host          = host.trim(),
                        port          = portVal,
                        user          = user.trim(),
                        password      = password,
                        mode          = mode,
                        sniHost       = sniHost.trim(),
                        proxyHost     = proxyHost.trim(),
                        proxyPort     = proxyPort.toIntOrNull() ?: 3128,
                        payloadEnabled = payloadEnabled,
                        payload       = payload,
                    ))
                },
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.textButtonColorsPrimary(),
            )
        }

        Spacer(Modifier.height(16.dp))
    }
}
