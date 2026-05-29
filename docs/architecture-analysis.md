# SSHCustom-VPNChain — Architecture Analysis
## v1.x/v4.x → v2.0 Rebuild Reference

---

## 1. What the v1.x/v4.x Codebase Does

SSHCustom-VPNChain is a **Magisk/KernelSU/APatch root module** for ARM64 Android that provides:

1. **SSH Tunnel with Payload Injection** — Connects to a remote SSH server using crafted HTTP payloads or SNI-wrapped TLS to bypass ISP restrictions on zero-rated/free-host carriers. No active mobile data plan needed.
2. **System-wide Transparent Proxy** — Routes all device TCP traffic through the SSH tunnel using iptables `REDIRECT` and a `tun2proxy` TUN device, without Android's VpnService API.
3. **SOCKS5 Local Proxy** — Exposes SSH dynamic forwarding as a SOCKS5 listener on `127.0.0.1:1080`.
4. **WebUI Daemon** — A Go binary (`sshcustomd`) that starts at boot in idle mode, serves a web dashboard at `127.0.0.1:9190`, and exposes a REST + SSE API.
5. **VPN Chain** — Routes OpenVPN (Windscribe) traffic through the SSH tunnel using native OpenVPN `--socks-proxy` support + iptables fwmark-based policy routing.

---

## 2. v1.x/v4.x Component Map

### `cmd/sshcustomd/main.go` (~4000 lines)
- Single-file monolith (intentional for v1)
- **SSH Client**: three transport modes — `direct`, `http_proxy`, `tls_sni`, `http_proxy_tls_sni`
- **Payload Injection**: injects crafted HTTP headers before SSH handshake using template variables `[host]`, `[port]`, `[crlf]` etc.
- **SSH Channel Pool**: pre-warms N SSH channels to eliminate per-connection negotiation latency
- **SOCKS5 Listener**: RFC 1928 compliant, opens SSH channels from pool for each CONNECT request
- **Transparent Proxy**: SO_ORIGINAL_DST socket option for REDIRECT mode; IP_TRANSPARENT for TPROXY mode
- **tun2proxy subprocess**: spawns `tun2proxy` binary, manages TUN interface lifecycle
- **REST API**: `/api/v1/*` endpoints, JSON envelope `{api_version, ok, data/error}`
- **SSE pub/sub**: coalescing single-slot broadcast; every `state.set()` pushes full snapshot
- **State struct**: tracks SSH phase, pool health, network interface, metrics (CPU/RSS from /proc)
- **Memory tuning**: `GCPercent(200)` + 192MB GC limit + `sync.Pool` buffer pools (128KB/256KB)

### `internal/dnsx/dnsx.go`
- Android-aware DNS resolver
- Reads carrier DNS from `getprop net.dns1`, `getprop net.dns2` → makes raw UDP DNS queries directly
- Bypasses `dnsproxyd` (UID-whitelisted on Android; Go's net resolver fails)
- 5-minute in-process cache per hostname
- Falls back to subprocess (`getent`, `nslookup`, `ping -c1`) if UDP fails

### `internal/metrics/metrics.go`
- Reads `/proc/stat` → CPU%, `/proc/self/statm` → RSS, `/proc/meminfo` → system memory
- Returns zero on parse failure (defensive for Android `/proc` variability)

### `internal/webui/webui.go`
- `go:embed`-wrapped `index.html` + `favicon.svg`
- Prefers on-disk `<workdir>/webroot/index.html` over embedded for hot-editing

### `internal/apiv1/contracts.go`
- Typed JSON envelope contracts shared between daemon and any client

### `src/module/scripts/sshcustom.sh`
- Master lifecycle controller: `start`, `stop`, `restart`, `start-idle`, `network-pause`, `network-resume`, `boot-reset`, `status`
- Updates `module.prop` description with emoji indicators (🟢/🟡/🔴)
- Double `net_clean.sh` run on stop for reliability

### `src/module/scripts/net_clean.sh`
- Comprehensive iptables teardown for all versions (v2, v3 TPROXY, v4)
- Removes `SSHC_*` chains from nat, mangle, filter tables (IPv4 + IPv6)
- Removes SSHC_OUTPUT, SSHC_PREROUTING, all legacy variants
- Restores IPv6, flushes conntrack, kills tun2proxy, deletes `tun_sshc` device
- Safe to run multiple times

### `src/module/scripts/tun_setup.sh`
- Post-SSH-connect: disables IPv6 system-wide (all traffic must be IPv4 through tunnel)
- Blocks QUIC (UDP/443) via iptables DROP for non-root UIDs
- Flushes conntrack

### `src/module/scripts/sshcustom_watchdog.sh`
- 10s tick loop, active-lite mode (only acts on route changes)
- Token file ensures only one instance active
- Marks network-paused on route loss; calls network-resume when route returns

### `src/module/service.sh`
- Boot hook: waits `sys.boot_completed=1` → `boot-reset` → `start-idle`
- If autostart marker present: waits for route (30s cap) → calls `/api/v1/control` start via curl/wget

### `src/module/vpnchain/vpnchain.sh`
- VPN Chain controller (see `docs/vpnchain-future.md` for detailed architecture)

---

## 3. Traffic Flow (v1.x/v4.x)

```
Normal SSH Tunnel:
  Device TCP → iptables nat REDIRECT → SSHC_OUTPUT chain → redir_port(9797)
  → sshcustomd transparent listener → SO_ORIGINAL_DST → SSH channel → VPS → Internet

OR (with tun2proxy):
  Device TCP/UDP → tun_sshc TUN device → tun2proxy → SOCKS5:1080
  → sshcustomd SOCKS5 listener → SSH channel → VPS → Internet

VPN Chain (OpenVPN over SSH):
  Apps (UID≥10000) → iptables mangle fwmark 0x1 → policy table 200 → tun0 (OpenVPN)
  → openvpn --socks-proxy 127.0.0.1:1080 → sshcustomd SOCKS5 → SSH channel → VPS
  → Windscribe server → Internet
```

---

## 4. iptables Chain Architecture (v1.x/v4.x)

All chains prefixed `SSHC_` to avoid collisions with other modules.

```
nat OUTPUT     → SSHC_OUTPUT  → (daemon bypass, subnet bypass, REDIRECT to redir_port)
nat PREROUTING → SSHC_PREROUTING → (hotspot interfaces, REDIRECT)
filter OUTPUT  → (QUIC DROP for non-root when quic=disable)
```

The v1.x implementation only used `redirect` mode (nat REDIRECT). No TPROXY or TUN mode was implemented in the shell layer — tun2proxy was launched as a subprocess by the Go daemon directly.

---

## 5. Key Design Decisions (v1.x/v4.x)

| Decision | Rationale |
|---|---|
| Daemon-always-alive (idle mode) | WebUI accessible at boot regardless of tunnel state |
| SSE coalescing (size-1 channel) | Slow clients get latest full snapshot; never blocks broadcaster |
| Android DNS workaround (dnsx) | Go's net resolver fails on Android due to dnsproxyd UID restriction |
| GC tuning (GCPercent=200, 192MB limit) | Reduces GC pauses during high-throughput TCP copying |
| sync.Pool buffer pools | Eliminates per-stream allocation overhead for 128KB/256KB copy buffers |
| Single VERSION file | Flows to module.prop, ldflags, CI artifacts, release tags |
| Shell delegation for iptables | Complex routing stays debuggable on-device |
| tun2proxy as subprocess | Avoids VpnService API entirely; pure root TUN creation |

---

## 6. How v2.0 Differs

The v2.0 rebuild restructures into a clean **monorepo** with three components:

### Directory Change
```
Old: src/module/ + cmd/sshcustomd/ (flat)
New: module/ + daemon/ + app/ (monorepo with clear separation)
```

### Module Shell Scripts
- Modeled directly after **boxproxy/box** (`box.service`, `box.iptables`, `box.tool`)
- `settings.ini` replaces `config.json` (shell-native key=value format, no JSON parser needed)
- `ssh.iptables` replaces `net_clean.sh` + `tun_setup.sh` with full capability probe system
- Four traffic modes: `redirect`, `tproxy`, `tun`, `tun_udpgw`
- All chains prefixed `SSHC_` (same prefix, maintains compatibility with net_clean.sh logic)
- Runtime snapshot at `run/runtime.iptables.env` — enables targeted cleanup on stop
- Capability probe caches in `run/iptables.cap.env`, invalidated per boot via `/proc/sys/kernel/random/boot_id`

### Go Daemon
- Split from monolith into clean internal packages: `internal/ssh`, `internal/proxy`, `internal/config`, `internal/api`
- **Unix socket API** replaces HTTP-only API for Android app communication
- HTTP REST API retained for WebUI compatibility
- Target: `GOOS=android GOARCH=arm64 CGO_ENABLED=0` (was `GOOS=linux`)
- SSH transport modes consolidated: `direct`, `sni`, `sni_http_proxy`

### Android App
- **New component** — did not exist in v1.x
- Kotlin + Jetpack Compose + miuix theme
- 4 tabs: Home (status+control), Profiles (SSH configs), Settings (mode/DNS/speed), Logs
- libsu RootService for all privileged operations
- Unix socket communication for real-time daemon stats

### Build System
- `tun2proxy` now compiled from source in CI (Rust cross-compile for aarch64-linux-android)
- Android APK built in same CI pipeline
- Go daemon target changes to `GOOS=android`

### VPN Chain
- **Deferred to future version** — fully documented in `docs/vpnchain-future.md`
- vpnchain.sh logic preserved and documented for future implementation

---

## 7. Critical Invariants Preserved in v2.0

1. `SSHC_` chain prefix retained — `net_clean.sh`-style cleanup logic remains valid
2. Work dir path: `/data/adb/sshcustom` (module installs to `/data/adb/sshcustom-vpnchain` but data at same legacy path for migration compatibility)
3. SOCKS5 on `127.0.0.1:1080` — VPN Chain depends on this
4. tun2proxy bypass mark `0x2` convention preserved
5. `sshcustom.sh` control interface preserved (start/stop/restart/status) for KSU Action compatibility
