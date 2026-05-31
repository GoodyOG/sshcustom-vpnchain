# Changelog

All notable changes to SSHCustom_Magisk are recorded here. Format is loosely
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
