package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.*
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
import java.util.UUID

@Composable
fun ProfilesScreen(
    profiles: List<Profile>,
    activeProfileId: String,
    onSelectProfile: (String) -> Unit,
    onSaveProfile: (Profile) -> Unit,
    onDeleteProfile: (String) -> Unit,
) {
    var showEditor by remember { mutableStateOf(false) }
    var editingProfile by remember { mutableStateOf<Profile?>(null) }

    Box(Modifier.fillMaxSize()) {
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(16.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp)
        ) {
            items(profiles) { profile ->
                ProfileCard(
                    profile = profile,
                    isActive = profile.id == activeProfileId,
                    onSelect = { onSelectProfile(profile.id) },
                    onEdit = { editingProfile = profile; showEditor = true },
                    onDelete = { onDeleteProfile(profile.id) }
                )
            }
            if (profiles.isEmpty()) {
                item {
                    Box(Modifier.fillMaxWidth().padding(48.dp), contentAlignment = Alignment.Center) {
                        Text("No profiles yet.\nTap + to add one.", color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f))
                    }
                }
            }
        }

        FloatingActionButton(
            onClick = { editingProfile = null; showEditor = true },
            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
            containerColor = MaterialTheme.colorScheme.primary
        ) {
            Icon(Icons.Default.Add, contentDescription = "Add profile")
        }
    }

    if (showEditor) {
        ProfileEditorSheet(
            initial = editingProfile,
            onSave = { profile ->
                onSaveProfile(profile)
                showEditor = false
            },
            onDismiss = { showEditor = false }
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
    val border = if (isActive) BorderStroke(1.5.dp, MaterialTheme.colorScheme.primary) else null
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        border = border,
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)
    ) {
        Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(profile.name, fontWeight = FontWeight.SemiBold, fontSize = 15.sp)
                Text("${profile.host}:${profile.port}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.6f))
                Text(profile.mode.uppercase(), fontSize = 11.sp, color = MaterialTheme.colorScheme.primary)
            }
            if (!isActive) {
                TextButton(onClick = onSelect) { Text("Select") }
            } else {
                Text("✓ Active", fontSize = 12.sp, color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.Medium)
            }
            IconButton(onClick = onEdit) { Text("✏️") }
            IconButton(onClick = onDelete) {
                Icon(Icons.Default.Delete, contentDescription = "Delete", tint = MaterialTheme.colorScheme.error)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ProfileEditorSheet(
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
    var showPassword by remember { mutableStateOf(false) }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier.padding(horizontal = 20.dp).padding(bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            Text(if (initial == null) "New Profile" else "Edit Profile", fontSize = 18.sp, fontWeight = FontWeight.SemiBold)

            OutlinedTextField(value = name, onValueChange = { name = it }, label = { Text("Profile Name") }, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(value = host, onValueChange = { host = it }, label = { Text("Server Host") }, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(value = port, onValueChange = { port = it }, label = { Text("Port") }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth())
            OutlinedTextField(value = user, onValueChange = { user = it }, label = { Text("Username") }, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(
                value = password, onValueChange = { password = it }, label = { Text("Password") },
                visualTransformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation(),
                trailingIcon = { TextButton(onClick = { showPassword = !showPassword }) { Text(if (showPassword) "Hide" else "Show") } },
                modifier = Modifier.fillMaxWidth()
            )

            // SSH Mode selector
            Text("SSH Mode", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.7f))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("direct" to "Direct", "sni" to "SNI", "sni_http_proxy" to "SNI+Proxy").forEach { (value, label) ->
                    FilterChip(selected = mode == value, onClick = { mode = value }, label = { Text(label) })
                }
            }

            if (mode == "sni" || mode == "sni_http_proxy") {
                OutlinedTextField(value = sniHost, onValueChange = { sniHost = it }, label = { Text("SNI Host") }, modifier = Modifier.fillMaxWidth())
            }
            if (mode == "sni_http_proxy") {
                OutlinedTextField(value = proxyHost, onValueChange = { proxyHost = it }, label = { Text("HTTP Proxy Host") }, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(value = proxyPort, onValueChange = { proxyPort = it }, label = { Text("HTTP Proxy Port") }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth())
            }

            // Payload
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = payloadEnabled, onCheckedChange = { payloadEnabled = it })
                Text("Enable Payload Injection")
            }
            if (payloadEnabled) {
                OutlinedTextField(
                    value = payload, onValueChange = { payload = it },
                    label = { Text("Payload") },
                    supportingText = { Text("Variables: [host] [port] [crlf] [cr] [lf]") },
                    minLines = 3, modifier = Modifier.fillMaxWidth()
                )
            }

            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.fillMaxWidth()) {
                OutlinedButton(onClick = onDismiss, modifier = Modifier.weight(1f)) { Text("Cancel") }
                Button(
                    onClick = {
                        onSave(Profile(
                            id = initial?.id ?: UUID.randomUUID().toString(),
                            name = name, host = host, port = port.toIntOrNull() ?: 22,
                            user = user, password = password, mode = mode,
                            sniHost = sniHost, proxyHost = proxyHost,
                            proxyPort = proxyPort.toIntOrNull() ?: 3128,
                            payloadEnabled = payloadEnabled, payload = payload
                        ))
                    },
                    modifier = Modifier.weight(1f)
                ) { Text("Save") }
            }
        }
    }
}
