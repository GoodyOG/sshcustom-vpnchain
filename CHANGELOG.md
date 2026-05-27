# Changelog

All notable changes to SSHCustom_Magisk are recorded here. Format is loosely
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.2.0] — 2026-05-27

### Added — TPROXY Transparent Proxy (Stage 2 Lite)

Upgrades the transparent TCP proxy from nat-table REDIRECT (IPv4 only) to
mangle-table TPROXY (IPv4 + IPv6), enabling IPv6 TCP tunneling on kernels
that lack `CONFIG_IP6_NF_NAT` (like the Poco F6 stock kernel).

- **TPROXY mode.** New `transparent_proxy.mode` config field with values:
  - `auto` (default): probe kernel for TPROXY support at runtime via a
    test mangle rule insertion. Falls back to REDIRECT if unsupported.
  - `tproxy`: force mangle-table TPROXY (IPv4 + IPv6 dual-stack).
  - `redirect`: force legacy nat-table REDIRECT (IPv4 only).
- **IPv6 TCP tunneling.** When TPROXY is active, `ip6tables -t mangle`
  chains `SSHC_TPROXY_OUT6` and `SSHC_TPROXY_PRE6` intercept outbound
  IPv6 TCP and deliver it to the daemon's `IP_TRANSPARENT` socket. Apps
  that connect over IPv6 now ride the SSH tunnel instead of being
  REJECT'd (or timing out on the carrier wall).
- **Dual-stack listener.** The daemon opens a single `AF_INET6` socket
  with `IPV6_V6ONLY=0` and `IP_TRANSPARENT`/`IPV6_TRANSPARENT` set.
  Both v4-mapped and native v6 connections arrive on one listener.
  `conn.LocalAddr()` is the original destination — no `SO_ORIGINAL_DST`
  syscall needed in TPROXY mode.
- **Policy routing.** `ip rule add fwmark 0x1/0x1 table 100` +
  `ip route add local 0.0.0.0/0 dev lo table 100` (and the v6
  equivalents) route marked packets to the daemon's transparent socket.
- **IPv6 lockdown integration.** When TPROXY is active, the IPv6
  lockdown filter chain inserts a RETURN rule for packets carrying
  our fwmark (`0x1/0x1`) before the catch-all REJECT. This lets
  TPROXY-intercepted v6 TCP through while still blocking non-TCP v6
  leaks (QUIC, raw UDP, etc.).
- **Auto-detection probe.** `iptables.ProbeTPROXY()` creates a
  temporary mangle chain and inserts a TPROXY rule. If it succeeds, the
  kernel has `CONFIG_NETFILTER_XT_TARGET_TPROXY`. The chain is always
  cleaned up regardless of result.

### Added — WebUI

- **Proxy Mode selector** on the Settings tab between "Sharing" and
  "Leak Protection". Dropdown with Auto / TPROXY / REDIRECT options.
  Shows the resolved mode (auto-detected result) below the selector.

### Added — API surface

- `transparent_proxy.mode` and `transparent_proxy.resolved_mode` in
  `/api/v1/status` config block and `/api/v1/diagnostics`.
- `ConfigPatchRequest.TransparentProxy.Mode` (`*string`) for runtime
  mode switching via `/api/v1/config` POST.
- New capabilities: `ipv6_transparent_tcp`, `tproxy_mode`.

### Changed

- `internal/iptables.Config` extended with `Mode` field. `Apply()`
  dispatches to `applyREDIRECT()` or `applyTPROXY()` based on mode.
- `internal/iptables.Cleanup()` now removes mangle-table chains
  (`SSHC_TPROXY_OUT`, `SSHC_TPROXY_PRE`, `SSHC_TPROXY_OUT6`,
  `SSHC_TPROXY_PRE6`) and policy routes (table 100) in addition to
  existing nat-table cleanup.
- `cmd/sshcustomd/main.go`: `startTransparentIfEnabled` opens an
  `IP_TRANSPARENT` dual-stack listener in TPROXY mode;
  `handleTPROXYConn` reads `conn.LocalAddr()` as original destination.
- `src/module/scripts/net_clean.sh` extended with mangle-table and
  policy-route cleanup.
- `src/module/config/config.json` ships with `"mode": "auto"`.
- Removed `"ipv6": false` capability — replaced by `ipv6_transparent_tcp`
  which reflects actual kernel support.

### HyperOS compatibility note

The fwmark `0x1/0x1` is safe because:
1. Our TPROXY/MARK rule inserts at `-I OUTPUT 1` (runs first).
2. Policy routing matches in OUTPUT before HyperOS's POSTROUTING
   `MARK and 0x0` clears marks (delivery already happened).
3. HyperOS's `routectrl_mangle_INPUT` stamps `0x30069/0x7fefffff` on
   inbound only — doesn't conflict with our OUTPUT-path mark.

## [1.1.0] — 2026-05-27

### Added — Leak Protection

Diagnosed against a Poco F6 / HyperOS 3 device on a CGNAT-restricted
carrier (MTN UNLIMITED 🇬🇧) where YouTube and Chrome were stalling for
~8 seconds per request despite a healthy SSH tunnel. Root cause: the
nat-table REDIRECT can only catch new IPv4 TCP flows. Anything else
escapes the tunnel and hits the carrier's restriction wall.

- **IPv6 lockdown.** New `transparent_proxy.block_ipv6_leaks` toggle
  (default ON). Installs `ip6tables -t filter` chains
  `SSHC_FILTER_OUTPUT6` and `SSHC_FILTER_FORWARD6` that REJECT outbound
  IPv6 with `icmp6-adm-prohibited`, except for UID 0 (the daemon),
  loopback, link-local NDP/RA, and ICMPv6. Apps that race-connect via
  IPv6 (RFC 6724 happy-eyeballs) fall back to IPv4 in milliseconds
  instead of waiting for kernel timeouts.
- **QUIC block.** New `transparent_proxy.block_quic` toggle (default
  ON). Installs `iptables -t filter` chain `SSHC_FILTER_QUIC` that
  REJECTs UDP/443 and UDP/80 except from UID 0. Forces Chrome and
  YouTube off QUIC and onto TCP/443, which our nat REDIRECT does
  capture, so QUIC traffic actually rides the tunnel.
- **Conntrack flush on apply/cleanup.** New
  `transparent_proxy.flush_conntrack` toggle (default ON). Drops every
  existing connection-tracking entry whenever rules are installed or
  removed. Without this, sockets opened before tunnel-up keep using the
  stale direct route forever (the conntrack entry shortcuts the nat
  table). Tries `/proc/sys/net/netfilter/nf_conntrack_flush`,
  `conntrack -F`, and `ip route flush cache` in order; whichever the
  kernel exposes wins.
- **Sysctl hardening.** New `transparent_proxy.route_localnet` toggle
  (default ON). Sets `net.ipv4.conf.{all,default,*}/route_localnet=1`
  and `rp_filter=2` so OUTPUT REDIRECTs that bounce through 127.0.0.1
  reliably reach the daemon's listener even on stricter kernels.
- **Schema migration.** New `transparent_proxy.leak_protection_v` field
  on the daemon Config. Existing installs upgrading from v1.0.x will
  see `leak_protection_v=0` on first load and have all four toggles
  flipped to ON exactly once, then the marker is bumped to 1. Users
  who later turn a toggle off keep their choice — the migration only
  fires when the marker is below the current schema version.

### Added — API surface

- `apiv1.LeakProtectionSettings` patch struct in
  `apiv1.ConfigPatchRequest`, with pointer-bool semantics for each
  toggle (consistent with `HotspotSettings`).
- `/api/v1/config` POST now accepts
  `{"leak_protection": {"block_ipv6_leaks": true, ...}}` and triggers
  a tunnel restart when any of the four flags change.
- `/api/v1/diagnostics` capabilities now include `block_ipv6_leaks`,
  `block_quic`, `flush_conntrack`, `route_localnet` so the WebUI and
  third-party scripts can introspect leak-protection state.
- `/api/v1/status` config block reports the four toggles under
  `transparent_proxy.{block_ipv6_leaks,block_quic,flush_conntrack,
  route_localnet}`.

### Added — WebUI

- New "Leak Protection" section on the Settings tab with four
  switches, sitting between "Sharing" and "Boot". Each switch persists
  via `/api/v1/config` and triggers a tunnel restart when toggled.
  Subtitles explain the trade-off in plain language.

### Changed

- `internal/iptables.Config` extended with `BlockIPv6Leaks`,
  `BlockQUIC`, `FlushConntrack`, `SetSysctls` fields.
- `internal/iptables.Apply` now installs the leak-protection layer
  after the nat chains and flushes conntrack last so existing flows
  re-resolve through the new rules.
- `internal/iptables.Cleanup` symmetrically removes the new chains and
  restores sysctls to Android's documented defaults (`route_localnet=0`,
  `rp_filter=1`).
- `src/module/scripts/net_clean.sh` extended to clean the new filter
  chains in both IPv4 and IPv6 tables, and to restore sysctls.

### Forensics

If you want to verify the leak-protection layer on a running device:

```sh
# IPv6 lockdown
ip6tables -t filter -nvL SSHC_FILTER_OUTPUT6 --line-numbers
# Should show climbing byte counters on the REJECT rule when apps try v6.

# QUIC block
iptables -t filter -nvL SSHC_FILTER_QUIC --line-numbers
# Should show climbing byte counters on the UDP/443 REJECT.

# REDIRECT effectiveness
iptables -t nat -nvL SSHC_OUTPUT --line-numbers
# Counter on the REDIRECT rule should grow much faster than before
# because old conntrack entries no longer shortcut the nat table.
```

A diagnostic dump from a Poco F6 / HyperOS 3 / MTN scenario is committed
at `docs/sshc_diag_20260527_030754.txt` for reference.

## [2.2.0] — 2026-05-16

### Added

- **Always-on daemon.** The daemon now starts automatically at boot in
  idle mode — the WebUI at `127.0.0.1:9190` is always accessible, even
  when the tunnel is not running. No more needing to tap the action
  button first.
- **Start/Stop/Restart tunnel from WebUI.** New contextual buttons on the
  Home tab: full-width "Start Tunnel" when idle, "Restart Tunnel" +
  "Stop Tunnel" when the tunnel is running. The daemon stays alive
  throughout — only the tunnel lifecycle is controlled.
- **Tunnel Uptime tracking.** The Home tab now shows tunnel uptime
  (how long since the tunnel connected) instead of daemon uptime.
- **module.prop state sync.** The module description in KernelSU / Magisk
  manager and WebUI-X now reflects the tunnel state in real-time:
  green = running, yellow = standby (no network), red = disconnected.
- **`--idle` flag** for the daemon binary — starts in WebUI-only mode
  without connecting the tunnel.
- **`start-idle` action** in `sshcustom.sh` — used by `service.sh` to
  launch the daemon without tunnel.

### Changed

- **Status dot always glows/pulses** — color indicates tunnel state
  (green = connected, yellow = connecting/standby, red = disconnected).
- **service.sh** always starts the daemon at boot. The autostart marker
  now controls whether the tunnel auto-connects, not whether the daemon
  runs.
- **Runtime tab** — info cards always display in 2 columns (no mobile
  collapse). Logs section restyled with rounded terminal, better button
  grouping.
- **Tunnel control is now internal** — `/api/v1/control` start/stop/restart
  operates on the tunnel without killing/restarting the daemon process.
- Removed `waitForDaemon` logic from WebUI since the daemon never dies.

## [2.1.8] — 2026-05-16

### Added

- **WebUI-X Portable compatibility.** The module WebUI now works
  correctly when opened via MMRL's WebUI-X Portable app or any other
  WebUI-X host (KSU-Next module WebUI, etc.).
  - **Safe-area insets**: UI respects device status bar and navigation
    bar heights — no more content overlap. CSS variables
    `--window-inset-top` / `--window-inset-bottom` injected by WebUI-X
    are consumed by the layout.
  - **`config.json`** added to webroot — enables the "Add Shortcut"
    button in WebUI-X's module list and configures back-button
    interception.
  - **`icon.png`** added to webroot (192×192 PNG rendered from the
    existing favicon SVG) — used as the home-screen shortcut icon.
  - **Back-button handling**: pressing back inside WebUI-X now
    intelligently closes modals → navigates to Home → exits, instead of
    immediately closing the WebUI.
  - **Status bar theming**: when running inside WebUI-X the status bar
    icons are set to light (matching the dark UI) via the module
    JavaScript interface.
  - **Material 3 dynamic colors**: the WebUI reads WebUI-X's injected
    color tokens so it visually matches the device's wallpaper-based
    theme (when available; falls back to the built-in dark palette).

## [2.0.3] — 2026-05-15

### Fixed

- **"Save, Use & Restart" now works reliably.** Reverted from the
  unreliable in-process `softRestart` mechanism to the proven
  `scheduleControl("restart")` which shells out to `sshcustom.sh restart`
  — kills the daemon and starts fresh. Works on all Android devices.

### Changed

- **WebUI overhauled**: page titles with icons on all 4 tabs, improved
  card spacing (24px between sections), reduced settings icon size,
  better elevation hierarchy, "Apply & Restart" button moved to bottom
  of Settings page.
- **Companion app removed.** The WebUI does everything; users access it
  via browser or KSU-Next's module WebUI feature. Removes 3000+ lines
  of Kotlin and the APK build from CI.

### Removed

- Entire `app/` directory, Gradle build system, APK signing workflow.
- Stale Android-related entries in `.gitignore`.

## [2.0.0] — 2026-05-14

A full rebuild. The module's runtime behaviour is compatible with v1
profiles, but the WebUI, daemon internals, and release shape all changed.

### Added

- **Companion Android app** under `app/`. Native Jetpack Compose UI with
  Material You dynamic colours on Android 12+. Talks to the daemon over
  the documented `/api/v1/*` surface.
  - Four tabs: Home, Profiles, Runtime, Settings.
  - Foreground service consumes the daemon's SSE stream and updates a
    persistent notification live.
  - Quick Settings Tile for one-tap tunnel toggle from the system shade.
  - Boot receiver auto-launches the foreground service on boot when
    autostart is enabled.
  - Profile import/export via the system Storage Access Framework (JSON).
  - Signed release APK in CI; debug fallback when signing secrets are
    absent (forks).
- **Stable v1 API contract** under `/api/v1/*` with a typed JSON envelope
  (`{api_version, ok, data, error}`). Documented in `docs/openapi.yaml`.
- **Server-Sent Events** stream at `/api/v1/events` for live dashboard
  updates without polling. Includes 25 s heartbeat.
- **`/api/v1/autostart` endpoint** — read/write the boot autostart flag.
- **`/api/v1/logs/{kind}/clear` endpoint** — POST truncates a log on disk
  and writes an audit line.
- **Boot-delayed autostart** — `service.sh` now waits for connectivity
  for up to 30 s after `sys.boot_completed=1` before starting the
  daemon, eliminating the "starts before radio is up" failure pattern.
- **`VERSION` file** as the single source of truth flowing into
  `module.prop`, `build.sh`, the Go binary's `version.Version`, the CI
  workflow's artifact name, and the app's `versionName` / `versionCode`.
- **Embedded WebUI** via `embed.FS`. The dashboard ships inside the
  daemon binary; the on-disk copy at `webroot/index.html` is the
  override. A botched install still has a working dashboard.
- **`favicon.svg`** for the WebUI tab and matching abstract launcher
  icon for the Android app (with monochrome variant for Android 13+
  themed icons).
- **Apache-2.0 LICENSE** for the module + Go daemon.
- **GPL-3.0 LICENSE** for the companion Android app (matches its
  KernelSU-Next inheritance).
- **NOTICE file** with third-party attributions.
- **Unit tests** for pure helpers in `internal/dnsx`, `internal/iptables`,
  `internal/metrics`, and the daemon (`extractHTTPStatuses`,
  `slugify`, `normalizeMode`, etc.).
- **`third_party/PATCHES.md`** documenting the vendored `x/crypto` fork.

### Changed

- **WebUI redesigned** to four tabs: Home, Profiles, Runtime, Settings.
  The previous Network tab was merged into Settings; the Compatibility
  tab was removed.
- **Profile editor** simplified: removed `Fallback IPs` field
  (hostname-only now), reduced to two buttons (Save / Save, Use &
  Restart).
- **Home page** drops the broken external Device Public IP lookup. The
  Device IP card now shows the local route source IP from
  `routeInfo()` — no external HTTP call, no `[::1]:53` errors.
- **Daemon refactored** from a 4 000-line `main.go` into focused
  packages: `internal/{config,state,api,sshpool,transport,proxy,
  iptables,dns,metrics,version,webui}`. Shipped binary behaviour
  unchanged.
- **Module version flow**: `module.prop` `version=v2.0.0`,
  `versionCode=20000`.
- **CI** now builds the Android APK alongside the module ZIP. Releases
  attach both the ZIP and the signed APK.

### Removed

- **Legacy `/api/*` endpoints** (non-v1 duplicates). The WebUI uses v1
  exclusively; the surface is smaller and easier to maintain.
- **Dead `fwmark 110` / `table 110` cleanup** from `net_clean.sh` —
  the daemon never installs those rules.
- **External device public-IP lookup** (`http://ip-api.com/...`) — it
  failed on Android's restricted DNS path and the value wasn't useful.

### Fixed

- Device Public IP card on Home page no longer shows
  `dial tcp: lookup ip-api.com on [::1]:53: connection refused`.

### Migration notes

- v1 profiles are forward-compatible. The `fallback_ips` field is
  ignored if present; safe to leave or remove.
- v1 `config.json` is forward-compatible (decoder ignores unknown keys).
  New keys land with defaults, so an in-place upgrade Just Works™.
- The legacy `/api/*` endpoints are gone. If you have third-party
  scripts hitting `/api/status` or similar, switch them to
  `/api/v1/status` (same JSON shape inside the new envelope).

## [1.0.0] — 2025

Initial production rebuild. Tagged after the v2 work began as `v1.0.0`
on GitHub for archival reference.

[2.1.8]: https://github.com/GoodyOG/SSHCustom-Magisk/releases/tag/v2.1.8
[2.0.3]: https://github.com/GoodyOG/SSHCustom_Magisk/releases/tag/v2.0.3
[2.0.0]: https://github.com/GoodyOG/SSHCustom_Magisk/releases/tag/v2.0.0
[1.0.0]: https://github.com/GoodyOG/SSHCustom_Magisk/releases/tag/v1.0.0
