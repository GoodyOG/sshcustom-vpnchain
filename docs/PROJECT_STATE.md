# SSHCustom-VPNChain — Project State Report
**Last updated:** 2026-05-30 after v2.0.1 release  
**Repo:** https://github.com/GoodyOG/sshcustom-vpnchain  
**Latest release:** [v2.0.1](https://github.com/GoodyOG/sshcustom-vpnchain/releases/tag/v2.0.1)

---

## 1. What This Project Is

A personal **Magisk/KernelSU/APatch root module** + companion Android app for an ARM64 Android device.

The owner has **no active mobile data plan** — only an SSH tunnel works. The SSH tunnel exploits a carrier zero-rated/free-host loophole by injecting HTTP payloads or wrapping SSH in TLS/SNI to bypass ISP restrictions. All device TCP traffic is routed through the tunnel using iptables at the kernel level — no Android `VpnService` is used.

**Traffic flow:**
```
Android apps → iptables REDIRECT/TPROXY/TUN → sshcustomd → SSH server → Internet
```

**VPN Chain** (future feature — documented, not yet implemented):
```
Android apps → tun0 (OpenVPN) → SOCKS5:1080 → SSH tunnel → Windscribe VPN server → Internet
```

---

## 2. Repository Structure

```
sshcustom-vpnchain/
├── module/                        # Magisk/KSU/APatch module (shell)
│   ├── META-INF/com/google/android/
│   │   ├── update-binary          # Magisk installer stub
│   │   └── updater-script         # #MAGISK
│   ├── bin/
│   │   ├── sshcustomd             # Go daemon binary (ARM64, CI-built)
│   │   └── tun2proxy              # TUN proxy binary (ARM64, CI-built from Rust)
│   ├── scripts/
│   │   ├── ssh.service            # start/stop/restart/status lifecycle
│   │   ├── ssh.iptables           # iptables rules (4 modes + capability probe)
│   │   └── ssh.tool               # BBR, TCP buffers, cgroup, logging helpers
│   ├── vpnchain/
│   │   ├── auth.txt               # PLACEHOLDER ONLY — never put real credentials here
│   │   └── configs/               # Drop .ovpn files here for VPN Chain
│   ├── module.prop
│   ├── settings.ini               # Master config (SSH creds, traffic mode, etc.)
│   ├── service.sh                 # Boot entrypoint (starts daemon in idle mode)
│   ├── action.sh                  # KSU Action button handler
│   ├── customize.sh               # Magisk/KSU/APatch installer script
│   └── update.json
│
├── daemon/                        # Go 1.23 daemon
│   ├── cmd/sshcustomd/main.go     # Main: HTTP API, tunnel loop, lifecycle
│   ├── internal/
│   │   ├── api/unix.go            # Unix socket API for Android app
│   │   ├── config/config.go       # settings.ini parser (AtomicConfig for thread-safety)
│   │   ├── proxy/socks5.go        # SOCKS5 RFC-1928 proxy listener
│   │   ├── proxy/transparent.go   # Transparent proxy (SO_ORIGINAL_DST)
│   │   └── ssh/client.go          # SSH client: 3 transport modes + channel pool
│   ├── go.mod                     # module: github.com/GoodyOG/SSHCustom_Magisk
│   └── go.sum
│
├── app/                           # Android companion app
│   ├── src/main/java/com/sshcustom/vpnchain/
│   │   ├── MainActivity.kt        # Root composable — outer Scaffold + NavigationBar only
│   │   ├── MainViewModel.kt       # MVVM ViewModel — all state + RootService binding
│   │   ├── SSHCustomApp.kt        # Application — configures libsu Shell.Builder
│   │   ├── data/Repository.kt     # HTTP polling, SharedPreferences persistence
│   │   ├── domain/Models.kt       # DaemonStatus, Profile, AppSettings, TunnelState
│   │   ├── service/SSHControlService.kt  # libsu RootService — privileged shell ops
│   │   └── ui/
│   │       ├── screens/HomeScreen.kt      # Status, control buttons, info cards
│   │       ├── screens/ProfilesScreen.kt  # Profile list + SuperBottomSheet editor
│   │       ├── screens/SettingsScreen.kt  # All settings + mode selectors
│   │       ├── screens/LogsScreen.kt      # Daemon log viewer + FAB
│   │       └── theme/Theme.kt             # MiuixTheme wrapper (dark default)
│   ├── build.gradle.kts           # AGP 8.7.3, compileSdk 35, arm64-only, R8 full mode
│   ├── gradle.properties          # android.enableR8.fullMode=true
│   ├── proguard-rules.pro         # R8 rules for miuix, libsu, kotlinx.serialization
│   └── gradle/libs.versions.toml  # AGP 8.7.3, Kotlin 2.1.0
│
├── .github/workflows/build.yml    # CI: daemon + tun2proxy + ZIP + release APK
├── docs/
│   ├── PROJECT_STATE.md           # THIS FILE — read before making any changes
│   ├── architecture-analysis.md   # v1→v2 architecture reference
│   └── vpnchain-future.md         # Complete VPN Chain implementation handoff
├── VERSION                        # 2.0.1 — single source of truth for version
└── build.sh                       # Local build helper (daemon only, no APK)
```

---

## 3. Key Technology Decisions

| Decision | Choice | Why |
|---|---|---|
| UI framework | `top.yukonga.miuix.kmp:miuix:0.7.2` | MIUI/HyperOS native look. **Must stay at 0.7.2** — 0.9.x requires compileSdk 37 which GitHub Actions runners don't have |
| Root access | `com.github.topjohnwu.libsu:*:6.0.0` | libsu 6.x: core + service + io. RootService in `:root` process |
| compileSdk | 35 | Max that doesn't require SDK 37 (miuix 0.7.2 constraint) |
| Gradle | 8.9 | Compatible with AGP 8.7.3; 8.10+ needs different config |
| AGP | 8.7.3 | Stable; AGP 9.x has Kotlin plugin registration conflicts |
| R8 | Full mode | `android.enableR8.fullMode=true` — needed for <10MB APK |
| ABI split | arm64-v8a only | Device is ARM64; halves APK size |
| Lint | Disabled for release | `checkReleaseBuilds = false` — lint crashes with AGP 8.7.3 + Kotlin 2.1.0 metadata mismatch |
| APK type | Release (signed) | CI generates a self-signed keystore; set `KEYSTORE_PASS` repo secret to pin it |
| Compose BOM | `2024.06.00` | miuix 0.7.2 doesn't transitively export foundation/layout for Android — explicit deps needed |

---

## 4. Module Scripts Architecture

### `settings.ini`
Shell-sourced config file. All scripts `.` (source) it. Contains the `log()` function used by all scripts. Key fields:
- `ssh_host`, `ssh_port`, `ssh_user`, `ssh_password`
- `ssh_mode` — `direct | sni | sni_http_proxy`
- `network_mode` — `redirect | tproxy | tun | tun_udpgw`
- `quic="disable"` — drops UDP 443/80 (forces TCP through tunnel)
- All paths: `box_dir=/data/adb/sshcustom`, `box_run`, `box_pid`, etc.

### `ssh.service` — Lifecycle manager
```
start       → speed_boost → start_daemon → cgroup_blkio → ssh.iptables enable
start-idle  → start_daemon --idle (WebUI only, no iptables)
stop        → ssh.iptables disable → kill daemon → speed_restore
restart     → stop + sleep 2 + start
status      → check PID alive + API health
```
**Important:** iptables failure is a WARNING, not hard abort. Tunnel stays up even if iptables has issues.

### `ssh.iptables` — Traffic rules
Four modes, all using `SSHC_` prefixed chains:
- `redirect` — nat REDIRECT to redir_port (TCP only, default)
- `tproxy` — mangle TPROXY (TCP+UDP, needs kernel support; auto-downgrades to redirect if unavailable)
- `tun` — mangle mark + policy routing → tun device
- `tun_udpgw` — same as tun + UDP gateway server

**Capability probe** caches to `run/state/iptables.cap.env`, invalidated by `boot_id`.  
**VPN Chain cleanup** always runs even if `vpnchain stop` wasn't called.

### `ssh.tool` — Helpers
- `blkio` — cgroup IO weight (non-fatal if not supported)
- `speed_boost` — BBR + TCP buffer tuning (saves originals first)
- `speed_restore` — restores original sysctl values
- `probe_user` — reads daemon UID/GID from /proc
- `boot_id` — POSIX-safe `tr -d '-'` (no `\n` escape, busybox-safe)

---

## 5. Go Daemon Architecture

**Binary:** `sshcustomd` — single static binary, `GOOS=android GOARCH=arm64 CGO_ENABLED=0`  
**Module path:** `github.com/GoodyOG/SSHCustom_Magisk` (legacy name, not changed)

### Key design decisions
- **AtomicConfig** (`internal/config/`) — config is loaded once and replaced atomically on SIGHUP. No mutex on individual fields — the whole struct is replaced.
- **SSH channel pool** (`internal/ssh/client.go`) — pre-warms `direct-tcpip` channels with proper RFC 4254 encoding. Uses `sshConn.DialContext()` for actual connections (correct extra-data encoding).
- **SSH keepalive** — `keepalive@openssh.com` every 30s, 3 missed = disconnect.
- **relay()** — uses `halfCloser` interface check for `CloseWrite()` — no `*net.TCPConn` type assertion that would panic on SSH channels.
- **waitSSHDrop** — uses `c.Wait()` not a polling loop.
- **runScriptTimeout** — all script calls have 30s `context.WithTimeout`.
- **HTTP server** — `ReadTimeout: 10s`, `WriteTimeout: 60s`, `IdleTimeout: 120s`.

### HTTP API (port 9190)
```
GET  /api/v1/health         → {"status":"ok","version":"..."}
GET  /api/v1/status         → runtime snapshot + config summary
POST /api/v1/control        → {"action": "start|stop|restart|start-idle"}
GET  /api/v1/autostart      → {"enabled": bool}
POST /api/v1/autostart      → {"enabled": bool}
GET  /api/v1/config         → full parsed config
GET  /                      → WebUI (on-disk webroot, or 404)
```

### Unix socket API (`run/sshcustomd.sock`)
World-readable (`chmod 0666`) so app process can connect without root.
```json
{ "type": "ping" }
{ "type": "status" }
{ "type": "control", "action": "start|stop|restart" }
```

---

## 6. Android App Architecture

### Navigation pattern
```
MainActivity → MainAppContent
  ├── Outer Scaffold (empty topBar, NavigationBar at bottom)
  │   → passes bottomPadding to each screen
  └── Screen composables (each owns its own Scaffold + TopAppBar + MiuixScrollBehavior)
      ├── HomeScreen
      ├── ProfilesScreen
      ├── SettingsScreen
      └── LogsScreen
```

**Why this pattern:** Each screen needs independent `MiuixScrollBehavior` for collapsing TopAppBar. The outer Scaffold only provides the shared NavigationBar + popup host.

### State management
- `MainViewModel` — single source of truth
- `DaemonRepository` — HTTP polling (1s), net speed (/proc/net/dev), WAN IP (60s), log reading
- Profiles, settings — persisted to `SharedPreferences` as JSON via `kotlinx.serialization`
- `TunnelState` — `sealed class` with `Starting`/`Stopping`/`Connected`/`Stopped`/`Error`; optimistic overrides held for 8s during ops

### Root access pattern
- `SSHControlService` extends `RootService` (libsu) — runs in `:root` process
- Bound via `RootService.bind()` in `MainViewModel.init()`
- All `sed` commands shell-escape values before interpolation (injection prevention)
- Falls back to raw `Shell.cmd()` if binder not connected yet

### miuix 0.7.2 component mapping
| What we use | miuix 0.7.2 class |
|---|---|
| Screen scaffold | `Scaffold` + `TopAppBar(scrollBehavior=MiuixScrollBehavior())` |
| Bottom nav | `NavigationBar(items, selected, onClick)` |
| Collapsing header | `TopAppBar` with `scrollBehavior` + `LazyColumn + nestedScroll(...)` |
| Toggle row | `SuperSwitch(checked, onCheckedChange, title, summary?)` |
| Tappable row with arrow | `SuperArrow(title, summary?, rightActions, onClick)` |
| Card container | `Card` |
| Section header | `SmallTitle(text)` |
| Button | `TextButton(text, onClick, colors)` |
| Primary button colors | `ButtonDefaults.textButtonColorsPrimary()` |
| Danger button colors | `ButtonDefaults.textButtonColors(color=error, textColor=White, ...)` |
| Bottom sheet (profile editor) | `SuperBottomSheet(show, title, onDismissRequest) { content }` |
| Text input | `TextField(value, onValueChange, label, singleLine)` |
| FAB | `FloatingActionButton { Text("↓") }` |
| Section title | `SmallTitle(text)` |

---

## 7. CI/CD Pipeline

### Workflow: `.github/workflows/build.yml`

```
push/tag → 5 parallel jobs:
  1. build-tun2proxy  → Rust cross-compile → artifact: tun2proxy-arm64
  2. build-daemon     → Go android/arm64  → artifact: sshcustomd-arm64
  3. package-module   → needs 1+2 → ZIP artifact
  4. build-apk        → assembleRelease with self-signed keystore → artifact: release APK
  5. release          → needs 3+4, tags only → GitHub Release
```

### APK signing in CI
- CI generates a `keytool` self-signed keystore at `/tmp/release.jks`
- Set **`KEYSTORE_PASS`** as a GitHub repo secret to use a consistent password
- Without the secret, falls back to `sshcustom_dev_keystore_2024`
- This is NOT a Play Store keystore — just enough to sign a release APK

### Expected artifact sizes
| Artifact | Expected size |
|---|---|
| `SSHCustom-VPNChain-v*.zip` | ~4-5 MB |
| `SSHCustom-VPNChain-release.apk` | ~6-8 MB |

---

## 8. Data Paths on Device

```
/data/adb/sshcustom/          # All module data (chmod 700, root only)
  settings.ini                # Master config — edit this to set SSH credentials
  bin/
    sshcustomd                # Go daemon (copied from module by customize.sh)
    tun2proxy                 # TUN proxy binary
  scripts/
    ssh.service               # Lifecycle manager
    ssh.iptables              # Traffic rules
    ssh.tool                  # Helper utilities
  run/
    sshcustom.pid             # Daemon PID
    sshcustom.log             # Main log (cleared on each start)
    control.log               # Service control log
    boot.log                  # Boot sequence log
    sshcustomd.sock           # Unix socket (chmod 0666, readable by app)
    autostart                 # Presence = autostart enabled
    state/
      runtime.iptables.env   # Saved mode/ports for targeted stop cleanup
      iptables.cap.env        # Capability probe cache (invalidated by boot_id)
  vpnchain/                   # VPN Chain data (future feature)
    auth.txt                  # Windscribe credentials (chmod 600)
    configs/                  # Drop .ovpn files here
    run/                      # VPN Chain runtime (logs, pids)
```

---

## 9. Known Limitations / Outstanding Work

### Not yet implemented
- **VPN Chain** — OpenVPN over SSH. Fully documented in `docs/vpnchain-future.md`. All architecture decisions, iptables strategy, auth.txt format, and API endpoints are spec'd out.
- **Profile persistence across app reinstall** — currently uses `SharedPreferences` which survives updates but not fresh reinstalls. Future: write directly to `settings.ini` as the single source of truth and read it back on start.
- **WAN IP via app HTTP client** — currently goes through loopback to daemon which proxies via SOCKS5. If tunnel is down, shows "—".
- **hotspot support** — module scripts have hooks but app has no UI for it.

### Known constraints
- **miuix must stay at 0.7.2** until either: GitHub Actions runners install SDK 37, or miuix publishes a version that supports SDK 35 again.
- **lint disabled** — `checkReleaseBuilds = false` because AGP 8.7.3 + Kotlin 2.1.0 causes a kotlin metadata parse crash in lintVitalRelease. Will fix when AGP 8.8+ is stable.
- **Release APK is self-signed** — cannot be distributed via Play Store, but sideloads fine.

---

## 10. Release History

| Version | Date | Key changes |
|---|---|---|
| `v1.0.1` | (preserved) | Original pre-rebuild tag |
| `v2.0.0` | 2026-05-30 | Complete monorepo rebuild: new module scripts, Go daemon v2, Android app from scratch |
| `v2.0.1` | 2026-05-30 | Profile editor scroll bug (SuperBottomSheet), collapsing TopAppBar, home icon, settings selection indicator, release APK (<8MB), R8 full mode |
| `v2.0.2` | 2026-05-30 | Profile editor fully scrollable (Column→LazyColumn inside sheet, fixes toggle-payload-then-can't-save), SSH Mode SuperDropdown, Settings Traffic Mode SuperDropdown, Settings DNS Mode SuperDropdown |
| `v2.0.3` | 2026-05-30 | Fix root detection: Shell.isAppGrantedRoot() + Shell.getShell().isRoot + FLAG_MOUNT_MASTER for KSU |
| `v2.0.4` | 2026-05-30 | Fix daemon idle→tunnel mode on Start; activeProfileId as StateFlow (instant select); multi-log tabs (Core/Control/Boot); nav bar inset on profile sheet; ports changed to 9799/9899/1081 (no box conflict); BasicComponent for non-nav About rows; boot-on-startup toggle; hotspot sharing toggle; bold info card values |

---

## 11. How to Build Locally

### Daemon (Go)
```bash
cd daemon
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build \
  -trimpath -buildvcs=false \
  -ldflags="-s -w -X main.version=$(cat ../VERSION | tr -d '[:space:]')" \
  -o ../module/bin/sshcustomd \
  ./cmd/sshcustomd/
```

### Module ZIP (no tun2proxy)
```bash
cd module
zip -r9 "../dist/SSHCustom-VPNChain-v$(cat ../VERSION | tr -d '[:space:]').zip" . \
  -x "*.DS_Store" -x "__MACOSX/*" -x "bin/.gitkeep" -x "vpnchain/configs/.gitkeep"
```

### APK
Requires Android SDK. Build via Android Studio or:
```bash
cd app && ./gradlew assembleRelease
```
Output: `app/build/outputs/apk/release/app-arm64-v8a-release.apk`

---

## 12. For the Next Session / AI Agent

### If the user wants to add a new feature:
1. Read `docs/vpnchain-future.md` if it's VPN Chain related
2. Read `docs/architecture-analysis.md` for overall architecture context
3. Check `VERSION` for current version number
4. The Go daemon is in `daemon/` — build with the command above to verify it compiles before pushing
5. The Android app uses **miuix 0.7.2** — do NOT upgrade to 0.9.x without first verifying SDK 37 is available on CI runners

### If the APK build breaks in CI:
- Most common cause: AGP/Gradle version compatibility. Current known-good: AGP 8.7.3 + Gradle 8.9 + Kotlin 2.1.0
- Second most common: `PaddingValues` imported from `androidx.compose.ui.unit` instead of `androidx.compose.foundation.layout`
- Third: lint crash — `checkReleaseBuilds = false` is already set

### If the module stops working on device:
1. Check `/data/adb/sshcustom/run/sshcustom.log` — the daemon log
2. Check `ssh.iptables disable` cleans properly — run it manually and check `iptables -t nat -L` is empty of SSHC_ chains
3. Check `settings.ini` has correct `ssh_host`, `ssh_user`, `ssh_password`
4. Check KSU Superuser has root granted to the app

### Critical invariants (do not break these):
1. `SSHC_` prefix on all iptables chains — collision prevention with other modules
2. `iptables enable` must be non-fatal — return 0 even on partial errors
3. `ssh.service stop` must leave device in 100% clean state
4. `miuix:0.7.2` — do not change this version without testing
5. `compileSdk = 35` — do not raise to 37 without confirming CI runners have SDK 37
6. `android.enableR8.fullMode=true` in gradle.properties — required for small APK
