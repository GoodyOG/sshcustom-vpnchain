package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch

@Composable
fun LogsScreen(
    logText: String,
    onClear: () -> Unit,
    onRefresh: () -> Unit,
) {
    val lines = remember(logText) { logText.lines() }
    val listState = rememberLazyListState()
    val scope = rememberCoroutineScope()
    var autoScroll by remember { mutableStateOf(true) }

    LaunchedEffect(lines.size) {
        if (autoScroll && lines.isNotEmpty()) {
            listState.animateScrollToItem(lines.size - 1)
        }
    }

    Column(Modifier.fillMaxSize()) {
        // Toolbar
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text("${lines.size} lines", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.5f))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = autoScroll, onCheckedChange = { autoScroll = it })
                    Text("Auto-scroll", fontSize = 12.sp)
                }
                OutlinedButton(onClick = onRefresh) { Text("Refresh") }
                OutlinedButton(
                    onClick = onClear,
                    colors = ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.error)
                ) { Text("Clear") }
            }
        }

        Box(
            Modifier.fillMaxSize()
                .background(Color(0xFF0D0D0D))
                .padding(horizontal = 8.dp)
        ) {
            LazyColumn(state = listState, modifier = Modifier.fillMaxSize()) {
                items(lines) { line ->
                    LogLine(line)
                }
            }
        }
    }
}

@Composable
private fun LogLine(line: String) {
    val color = when {
        line.contains("[Error]", ignoreCase = true)   -> Color(0xFFCF6679)
        line.contains("[Warning]", ignoreCase = true) -> Color(0xFFFFC107)
        line.contains("[Debug]", ignoreCase = true)   -> Color(0xFF757575)
        else                                           -> Color(0xFFE0E0E0)
    }
    Text(
        text = line,
        color = color,
        fontSize = 11.sp,
        fontFamily = FontFamily.Monospace,
        modifier = Modifier.fillMaxWidth().padding(vertical = 1.dp)
    )
}
