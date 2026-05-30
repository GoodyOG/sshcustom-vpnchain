package com.sshcustom.vpnchain.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import top.yukonga.miuix.kmp.basic.ButtonDefaults
import top.yukonga.miuix.kmp.basic.Text
import top.yukonga.miuix.kmp.basic.TextButton
import top.yukonga.miuix.kmp.theme.MiuixTheme

@Composable
fun LogsScreen(
    logText: String,
    onClear: () -> Unit,
    onRefresh: () -> Unit,
    paddingValues: PaddingValues,
) {
    val lines = remember(logText) { if (logText.isBlank()) emptyList() else logText.lines() }
    val listState = rememberLazyListState()
    var autoScroll by remember { mutableStateOf(true) }

    LaunchedEffect(lines.size) {
        if (autoScroll && lines.isNotEmpty()) {
            listState.animateScrollToItem(lines.size - 1)
        }
    }

    Column(Modifier.fillMaxSize().padding(paddingValues)) {
        // Toolbar
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            Text(
                text = "${lines.size} lines",
                fontSize = 12.sp,
                color = MiuixTheme.colorScheme.onSurfaceVariantActions,
                modifier = Modifier.weight(1f),
            )
            TextButton(
                text = if (autoScroll) "📌 Auto" else "Auto",
                onClick = { autoScroll = !autoScroll },
                modifier = Modifier.wrapContentWidth(),
            )
            TextButton(
                text = "Refresh",
                onClick = onRefresh,
                modifier = Modifier.wrapContentWidth(),
            )
            TextButton(
                text = "Clear",
                onClick = onClear,
                modifier = Modifier.wrapContentWidth(),
                colors = ButtonDefaults.textButtonColorsDanger(),
            )
        }

        Box(
            Modifier
                .fillMaxSize()
                .background(Color(0xFF0D0D0D))
                .padding(horizontal = 8.dp)
        ) {
            LazyColumn(state = listState, modifier = Modifier.fillMaxSize()) {
                if (lines.isEmpty()) {
                    item {
                        Text(
                            text = "(no logs yet)",
                            fontSize = 12.sp,
                            color = Color(0xFF555555),
                            modifier = Modifier.padding(16.dp)
                        )
                    }
                } else {
                    items(lines, key = { it.hashCode().toString() + lines.indexOf(it) }) { line ->
                        LogLine(line)
                    }
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
        else                                           -> Color(0xFFDDDDDD)
    }
    Text(
        text = line,
        color = color,
        fontSize = 11.sp,
        fontFamily = FontFamily.Monospace,
        modifier = Modifier.fillMaxWidth().padding(vertical = 1.dp)
    )
}
