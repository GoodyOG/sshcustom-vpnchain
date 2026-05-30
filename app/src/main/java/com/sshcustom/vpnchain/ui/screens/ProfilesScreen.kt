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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.sshcustom.vpnchain.domain.Profile
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.extra.SuperBottomSheet
import top.yukonga.miuix.kmp.extra.SuperDropdown
import top.yukonga.miuix.kmp.extra.SuperSwitch
import top.yukonga.miuix.kmp.theme.MiuixTheme
import java.util.UUID

// ── SSH mode helpers ──────────────────────────────────────────────────────────
private val SSH_MODE_LABELS  = listOf("Direct", "SNI (TLS + SNI spoof)", "SNI + HTTP Proxy")
private val SSH_MODE_VALUES  = listOf("direct", "sni", "sni_http_proxy")
private fun modeToIndex(mode: String) = SSH_MODE_VALUES.indexOf(mode).coerceAtLeast(0)
private fun indexToMode(i: Int)       = SSH_MODE_VALUES.getOrElse(i) { "direct" }

@Composable
fun ProfilesScreen(
    profiles: List<Profile>,
    activeProfileId: String,
    onSelectProfile: (String) -> Unit,
    onSaveProfile: (Profile) -> Unit,
    onDeleteProfile: (String) -> Unit,
    bottomPadding: PaddingValues,
) {
    val showEditor     = remember { mutableStateOf(false) }
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
                top   = innerPadding.calculateTopPadding() + 8.dp,
                bottom = bottomPadding.calculateBottomPadding() + 16.dp,
            ),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            item {
                TextButton(
                    text     = "+ Add Profile",
                    onClick  = { editingProfile = null; showEditor.value = true },
                    modifier = Modifier.fillMaxWidth(),
                    colors   = ButtonDefaults.textButtonColorsPrimary(),
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
                                color    = MiuixTheme.colorScheme.onSurfaceVariantSummary,
                                fontSize = 14.sp,
                            )
                        }
                    }
                }
            } else {
                itemsIndexed(profiles, key = { _, p -> p.id }) { _, p ->
                    ProfileCard(
                        profile  = p,
                        isActive = p.id == activeProfileId,
                        onSelect = { onSelectProfile(p.id) },
                        onEdit   = { editingProfile = p; showEditor.value = true },
                        onDelete = { onDeleteProfile(p.id) },
                    )
                }
            }
        }
    }

    // SuperBottomSheet is placed outside the screen Scaffold so the sheet's
    // own dismiss/drag gesture and the inner LazyColumn scroll don't conflict.
    if (showEditor.value) {
        SuperBottomSheet(
            show             = showEditor,
            title            = if (editingProfile == null) "New Profile" else "Edit Profile",
            onDismissRequest = { showEditor.value = false },
        ) {
            ProfileEditorContent(
                initial  = editingProfile,
                onSave   = { profile -> onSaveProfile(profile); showEditor.value = false },
                onDismiss = { showEditor.value = false },
            )
        }
    }
}

// ── Profile card (list item) ──────────────────────────────────────────────────

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
                    Text(profile.name, fontWeight = FontWeight.SemiBold, fontSize = 15.sp,
                        color = MiuixTheme.colorScheme.onSurface)
                    Text("${profile.host}:${profile.port}", fontSize = 12.sp,
                        color = MiuixTheme.colorScheme.onSurfaceVariantActions)
                    Text(profile.mode.uppercase(), fontSize = 11.sp,
                        color = MiuixTheme.colorScheme.primary)
                }
                if (isActive) {
                    Text("● Active", fontSize = 12.sp, color = MiuixTheme.colorScheme.primary,
                        fontWeight = FontWeight.Medium, modifier = Modifier.padding(end = 4.dp))
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (!isActive) {
                    TextButton(text = "Select", onClick = onSelect,
                        modifier = Modifier.weight(1f),
                        colors   = ButtonDefaults.textButtonColorsPrimary())
                }
                TextButton(text = "Edit",   onClick = onEdit,   modifier = Modifier.weight(1f))
                TextButton(
                    text     = "Delete",
                    onClick  = onDelete,
                    modifier = Modifier.weight(1f),
                    colors   = ButtonDefaults.textButtonColors(
                        color             = MiuixTheme.colorScheme.error,
                        textColor         = Color.White,
                        disabledColor     = MiuixTheme.colorScheme.disabledSecondaryVariant,
                        disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                    ),
                )
            }
        }
    }
}

// ── Profile editor (content inside SuperBottomSheet) ─────────────────────────
//
// FIX: The SuperBottomSheet wraps content in a non-scrolling Column that is
// constrained to (windowHeight - statusBarHeight). If the form content is also
// a plain Column, there is no scroll mechanism and items below the screen edge
// are unreachable.
//
// The solution: use a LazyColumn for the form content. The LazyColumn scrolls
// within the fixed-height sheet. The sheet's drag-handle gesture operates on
// the 24dp handle area at the top and does not conflict with the inner scroll.
//
@Composable
private fun ProfileEditorContent(
    initial: Profile?,
    onSave: (Profile) -> Unit,
    onDismiss: () -> Unit,
) {
    var name           by remember { mutableStateOf(initial?.name     ?: "") }
    var host           by remember { mutableStateOf(initial?.host     ?: "") }
    var port           by remember { mutableStateOf(initial?.port?.toString() ?: "22") }
    var user           by remember { mutableStateOf(initial?.user     ?: "") }
    var password       by remember { mutableStateOf(initial?.password ?: "") }
    var modeIndex      by remember { mutableIntStateOf(modeToIndex(initial?.mode ?: "direct")) }
    var sniHost        by remember { mutableStateOf(initial?.sniHost   ?: "") }
    var proxyHost      by remember { mutableStateOf(initial?.proxyHost ?: "") }
    var proxyPort      by remember { mutableStateOf(initial?.proxyPort?.toString() ?: "3128") }
    var payloadEnabled by remember { mutableStateOf(initial?.payloadEnabled ?: false) }
    var payload        by remember { mutableStateOf(initial?.payload  ?: "") }
    var showPwd        by remember { mutableStateOf(false) }
    var error          by remember { mutableStateOf("") }

    val currentMode = indexToMode(modeIndex)
    val showSniFields   = currentMode == "sni" || currentMode == "sni_http_proxy"
    val showProxyFields = currentMode == "sni_http_proxy"

    // LazyColumn — scrolls freely inside SuperBottomSheet's fixed-height container.
    LazyColumn(
        modifier            = Modifier.fillMaxWidth(),
        contentPadding      = PaddingValues(
            bottom = WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 28.dp,
        ),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {

        // Error banner
        if (error.isNotEmpty()) {
            item(key = "error") {
                Text(error, color = MiuixTheme.colorScheme.error, fontSize = 13.sp,
                    modifier = Modifier.padding(horizontal = 4.dp))
            }
        }

        // Basic fields
        item(key = "name") {
            TextField(value = name, onValueChange = { name = it; error = "" },
                label = "Profile Name *", singleLine = true, modifier = Modifier.fillMaxWidth())
        }
        item(key = "host") {
            TextField(value = host, onValueChange = { host = it.trim(); error = "" },
                label = "Server Host *", singleLine = true, modifier = Modifier.fillMaxWidth())
        }
        item(key = "port") {
            TextField(value = port,
                onValueChange = { port = it.filter { c -> c.isDigit() }.take(5) },
                label = "Port (1–65535)", singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth())
        }
        item(key = "user") {
            TextField(value = user, onValueChange = { user = it },
                label = "Username", singleLine = true, modifier = Modifier.fillMaxWidth())
        }
        item(key = "password") {
            TextField(value = password, onValueChange = { password = it },
                label = "Password", singleLine = true,
                visualTransformation = if (showPwd) VisualTransformation.None
                                       else PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth())
        }
        item(key = "showpwd") {
            TextButton(
                text     = if (showPwd) "Hide password" else "Show password",
                onClick  = { showPwd = !showPwd },
                modifier = Modifier.wrapContentWidth(),
            )
        }

        // SSH Mode — SuperDropdown (MIUI-style popup with checkmarks)
        item(key = "mode") {
            Card(Modifier.fillMaxWidth()) {
                SuperDropdown(
                    title               = "SSH Mode",
                    items               = SSH_MODE_LABELS,
                    selectedIndex       = modeIndex,
                    onSelectedIndexChange = { modeIndex = it },
                )
            }
        }

        // SNI host (conditional)
        if (showSniFields) {
            item(key = "snihost") {
                TextField(value = sniHost, onValueChange = { sniHost = it },
                    label = "SNI Host", singleLine = true, modifier = Modifier.fillMaxWidth())
            }
        }

        // HTTP proxy fields (conditional)
        if (showProxyFields) {
            item(key = "proxyhost") {
                TextField(value = proxyHost, onValueChange = { proxyHost = it },
                    label = "HTTP Proxy Host", singleLine = true,
                    modifier = Modifier.fillMaxWidth())
            }
            item(key = "proxyport") {
                TextField(value = proxyPort,
                    onValueChange = { proxyPort = it.filter { c -> c.isDigit() }.take(5) },
                    label = "HTTP Proxy Port", singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    modifier = Modifier.fillMaxWidth())
            }
        }

        // Payload toggle
        item(key = "payloadswitch") {
            SuperSwitch(
                checked           = payloadEnabled,
                onCheckedChange   = { payloadEnabled = it },
                title             = "Enable Payload Injection",
                summary           = "HTTP inject payload — vars: [host] [port] [crlf]",
            )
        }

        // Payload text field (conditional — only appears when toggle is ON)
        if (payloadEnabled) {
            item(key = "payloadfield") {
                TextField(
                    value         = payload,
                    onValueChange = { payload = it },
                    label         = "Payload",
                    modifier      = Modifier.fillMaxWidth(),
                    minLines      = 3,
                )
            }
        }

        // Cancel / Save buttons — always the last item so they're always reachable
        item(key = "buttons") {
            Row(
                modifier                = Modifier.fillMaxWidth().padding(top = 4.dp),
                horizontalArrangement   = Arrangement.spacedBy(12.dp),
            ) {
                TextButton("Cancel", onDismiss, modifier = Modifier.weight(1f))
                TextButton(
                    text     = "Save",
                    onClick  = {
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
                                mode           = currentMode,
                                sniHost        = sniHost.trim(),
                                proxyHost      = proxyHost.trim(),
                                proxyPort      = proxyPort.toIntOrNull() ?: 3128,
                                payloadEnabled = payloadEnabled,
                                payload        = payload,
                            )
                        )
                    },
                    modifier = Modifier.weight(1f),
                    colors   = ButtonDefaults.textButtonColorsPrimary(),
                )
            }
        }
    }
}
