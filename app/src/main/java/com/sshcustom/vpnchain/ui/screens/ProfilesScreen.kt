package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.PaddingValues
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.Profile
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.extra.SuperArrow
import top.yukonga.miuix.kmp.extra.SuperBottomSheet
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
    bottomPadding: PaddingValues,
) {
    val showEditor = remember { mutableStateOf(false) }
    var editingProfile by remember { mutableStateOf<Profile?>(null) }

    val scrollBehavior = MiuixScrollBehavior()

    Scaffold(
        topBar = { TopAppBar(title = "Profiles", scrollBehavior = scrollBehavior) },
    ) { innerPadding ->
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .nestedScroll(scrollBehavior.nestedScrollConnection),
            contentPadding = PaddingValues(
                start = 12.dp, end = 12.dp,
                top = innerPadding.calculateTopPadding() + 8.dp,
                bottom = bottomPadding.calculateBottomPadding() + 16.dp,
            ),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            item {
                TextButton(
                    text = "+ Add Profile",
                    onClick = { editingProfile = null; showEditor.value = true },
                    modifier = Modifier.fillMaxWidth(),
                    colors = ButtonDefaults.textButtonColorsPrimary(),
                )
            }

            if (profiles.isEmpty()) {
                item {
                    Card(Modifier.fillMaxWidth()) {
                        Box(
                            Modifier.fillMaxWidth().padding(32.dp),
                            contentAlignment = Alignment.Center,
                        ) {
                            Text(
                                "No profiles yet.\nTap '+ Add Profile' to create one.",
                                color = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                                fontSize = 14.sp,
                            )
                        }
                    }
                }
            } else {
                itemsIndexed(profiles, key = { _, p -> p.id }) { _, p ->
                    ProfileCard(
                        profile = p,
                        isActive = p.id == activeProfileId,
                        onSelect = { onSelectProfile(p.id) },
                        onEdit = { editingProfile = p; showEditor.value = true },
                        onDelete = { onDeleteProfile(p.id) },
                    )
                }
            }
        }
    }

    // SuperBottomSheet lives OUTSIDE the LazyColumn/Scaffold so it has
    // its own scroll context — this is the fix for the "can't scroll" bug.
    if (showEditor.value) {
        SuperBottomSheet(
            show = showEditor,
            title = if (editingProfile == null) "New Profile" else "Edit Profile",
            onDismissRequest = { showEditor.value = false },
        ) {
            ProfileEditorContent(
                initial = editingProfile,
                onSave = { profile -> onSaveProfile(profile); showEditor.value = false },
                onDismiss = { showEditor.value = false },
            )
        }
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
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(
                        profile.name,
                        fontWeight = FontWeight.SemiBold,
                        fontSize = 15.sp,
                        color = MiuixTheme.colorScheme.onSurface,
                    )
                    Text(
                        "${profile.host}:${profile.port}",
                        fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                    )
                    Text(
                        profile.mode.uppercase(),
                        fontSize = 11.sp,
                        color = MiuixTheme.colorScheme.primary,
                    )
                }
                if (isActive) {
                    Text(
                        "● Active",
                        fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.primary,
                        fontWeight = FontWeight.Medium,
                        modifier = Modifier.padding(end = 4.dp),
                    )
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (!isActive) {
                    TextButton(
                        text = "Select", onClick = onSelect,
                        modifier = Modifier.weight(1f),
                        colors = ButtonDefaults.textButtonColorsPrimary(),
                    )
                }
                TextButton(text = "Edit", onClick = onEdit, modifier = Modifier.weight(1f))
                TextButton(
                    text = "Delete", onClick = onDelete,
                    modifier = Modifier.weight(1f),
                    colors = ButtonDefaults.textButtonColors(
                        color = MiuixTheme.colorScheme.error, textColor = Color.White,
                        disabledColor = MiuixTheme.colorScheme.disabledSecondaryVariant,
                        disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                    ),
                )
            }
        }
    }
}

/**
 * Form content rendered inside SuperBottomSheet.
 * The sheet handles scroll internally — no Column+verticalScroll needed here.
 */
@Composable
private fun ProfileEditorContent(
    initial: Profile?,
    onSave: (Profile) -> Unit,
    onDismiss: () -> Unit,
) {
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
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = 16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        if (error.isNotEmpty()) {
            Text(error, color = MiuixTheme.colorScheme.error, fontSize = 13.sp,
                modifier = Modifier.padding(horizontal = 4.dp))
        }

        TextField(value = name, onValueChange = { name = it; error = "" },
            label = "Profile Name *", singleLine = true,
            modifier = Modifier.fillMaxWidth())
        TextField(value = host, onValueChange = { host = it.trim(); error = "" },
            label = "Server Host *", singleLine = true,
            modifier = Modifier.fillMaxWidth())
        TextField(value = port,
            onValueChange = { port = it.filter { c -> c.isDigit() }.take(5) },
            label = "Port", singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
            modifier = Modifier.fillMaxWidth())
        TextField(value = user, onValueChange = { user = it },
            label = "Username", singleLine = true, modifier = Modifier.fillMaxWidth())
        TextField(value = password, onValueChange = { password = it },
            label = "Password", singleLine = true,
            visualTransformation = if (showPwd) VisualTransformation.None
                                   else PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth())
        TextButton(
            text = if (showPwd) "Hide password" else "Show password",
            onClick = { showPwd = !showPwd },
            modifier = Modifier.wrapContentWidth(),
        )

        SmallTitle("SSH Mode")
        Card(Modifier.fillMaxWidth()) {
            listOf(
                "direct"        to "Direct",
                "sni"           to "SNI (TLS + SNI spoof)",
                "sni_http_proxy" to "SNI + HTTP Proxy",
            ).forEach { (m, label) ->
                SuperArrow(
                    title = label,
                    rightActions = {
                        if (mode == m) {
                            Text(
                                "✓",
                                color = MiuixTheme.colorScheme.primary,
                                fontSize = 16.sp,
                                fontWeight = FontWeight.Bold,
                            )
                        }
                    },
                    onClick = { mode = m },
                )
            }
        }

        if (mode == "sni" || mode == "sni_http_proxy") {
            TextField(value = sniHost, onValueChange = { sniHost = it },
                label = "SNI Host", singleLine = true, modifier = Modifier.fillMaxWidth())
        }
        if (mode == "sni_http_proxy") {
            TextField(value = proxyHost, onValueChange = { proxyHost = it },
                label = "HTTP Proxy Host", singleLine = true, modifier = Modifier.fillMaxWidth())
            TextField(value = proxyPort,
                onValueChange = { proxyPort = it.filter { c -> c.isDigit() }.take(5) },
                label = "HTTP Proxy Port", singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth())
        }

        SuperSwitch(
            checked = payloadEnabled,
            onCheckedChange = { payloadEnabled = it },
            title = "Enable Payload Injection",
            summary = "Paste HTTP inject payload — vars: [host] [port] [crlf]",
        )
        if (payloadEnabled) {
            TextField(
                value = payload, onValueChange = { payload = it },
                label = "Payload",
                modifier = Modifier.fillMaxWidth(),
                minLines = 3,
            )
        }

        Row(
            modifier = Modifier.fillMaxWidth().padding(top = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            TextButton("Cancel", onDismiss, modifier = Modifier.weight(1f))
            TextButton(
                "Save",
                onClick = {
                    if (name.isBlank() || host.isBlank()) {
                        error = "Name and host are required"; return@TextButton
                    }
                    val portInt = port.toIntOrNull()
                    if (portInt == null || portInt !in 1..65535) {
                        error = "Port must be 1–65535"; return@TextButton
                    }
                    onSave(
                        Profile(
                            id             = initial?.id ?: UUID.randomUUID().toString(),
                            name           = name.trim(),
                            host           = host.trim(),
                            port           = portInt,
                            user           = user.trim(),
                            password       = password,
                            mode           = mode,
                            sniHost        = sniHost.trim(),
                            proxyHost      = proxyHost.trim(),
                            proxyPort      = proxyPort.toIntOrNull() ?: 3128,
                            payloadEnabled = payloadEnabled,
                            payload        = payload,
                        )
                    )
                },
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.textButtonColorsPrimary(),
            )
        }
    }
}
