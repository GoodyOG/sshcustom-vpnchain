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
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import top.yukonga.miuix.kmp.basic.*
import top.yukonga.miuix.kmp.extra.SuperDropdown
import top.yukonga.miuix.kmp.theme.MiuixTheme

// Log file selector — keys match ViewModel.LOG_PATHS, labels shown in the dropdown
private val LOG_KEYS   = listOf("core", "boot", "tool", "openvpn")
private val LOG_LABELS = listOf("Core", "Boot", "Tool", "OpenVPN")

@Composable
fun LogsScreen(
    logText: String,
    activeLog: String,
    onSwitchLog: (String) -> Unit,
    onClear: () -> Unit,
    onRefresh: () -> Unit,
    bottomPadding: PaddingValues,
) {
    val lines = remember(logText) {
        if (logText.isBlank()) emptyList() else logText.lines()
    }
    val listState      = rememberLazyListState()
    var autoScroll     by remember { mutableStateOf(true) }
    val scrollBehavior = MiuixScrollBehavior()

    val atBottom by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()
            last == null || last.index >= lines.lastIndex
        }
    }

    LaunchedEffect(lines.size) {
        if (autoScroll && lines.isNotEmpty()) {
            listState.animateScrollToItem(lines.lastIndex)
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title   = "Logs",
                scrollBehavior = scrollBehavior,
                actions = {
                    TextButton(
                        text     = if (autoScroll) "Auto ●" else "Auto ○",
                        onClick  = { autoScroll = !autoScroll },
                        modifier = Modifier.wrapContentWidth(),
                    )
                    TextButton(
                        text     = "Clear",
                        onClick  = onClear,
                        modifier = Modifier.wrapContentWidth(),
                        colors   = ButtonDefaults.textButtonColors(
                            color             = MiuixTheme.colorScheme.error,
                            textColor         = Color.White,
                            disabledColor     = MiuixTheme.colorScheme.disabledSecondaryVariant,
                            disabledTextColor = MiuixTheme.colorScheme.disabledOnSecondaryVariant,
                        ),
                    )
                },
            )
        },
        floatingActionButton = {
            if (!atBottom) {
                FloatingActionButton(onClick = { autoScroll = true }) {
                    Text("↓", fontSize = 18.sp, color = Color.White)
                }
            }
        },
    ) { innerPadding ->
        Column(Modifier.fillMaxSize()) {

            // ── Log file selector ──────────────────────────────────────────────
            // Dropdown below the TopAppBar — pick Core / Boot / Tool.
            Spacer(Modifier.height(innerPadding.calculateTopPadding()))
            Card(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 6.dp),
            ) {
                SuperDropdown(
                    title                 = "Log",
                    items                 = LOG_LABELS,
                    selectedIndex         = LOG_KEYS.indexOf(activeLog).coerceAtLeast(0),
                    onSelectedIndexChange = { onSwitchLog(LOG_KEYS[it]) },
                )
            }

            // ── Log content ────────────────────────────────────────────────────
            Box(
                Modifier
                    .fillMaxSize()
                    .background(Color(0xFF0D0D0D)),
            ) {
                if (lines.isEmpty()) {
                    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text(
                            "(no logs — tap Refresh or wait 2s)",
                            fontSize = 13.sp,
                            color    = Color(0xFF555555),
                        )
                    }
                } else {
                    LazyColumn(
                        state = listState,
                        modifier = Modifier
                            .fillMaxSize()
                            .nestedScroll(scrollBehavior.nestedScrollConnection),
                        contentPadding = PaddingValues(
                            start  = 8.dp, end = 8.dp, top = 4.dp,
                            bottom = bottomPadding.calculateBottomPadding() + 16.dp,
                        ),
                    ) {
                        items(lines) { line -> LogLine(line) }
                    }
                }
            }
        }
    }
}

@Composable
private fun LogLine(line: String) {
    Text(
        text = line,
        color = when {
            line.contains("[Error]",   ignoreCase = true) -> Color(0xFFCF6679)
            line.contains("[Warning]", ignoreCase = true) -> Color(0xFFFFC107)
            line.contains("[Debug]",   ignoreCase = true) -> Color(0xFF757575)
            else                                           -> Color(0xFFDDDDDD)
        },
        fontSize   = 11.sp,
        fontFamily = FontFamily.Monospace,
        modifier   = Modifier.fillMaxWidth().padding(vertical = 1.dp),
    )
}
