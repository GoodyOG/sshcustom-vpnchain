# SSHCustom-VPNChain

Magisk/KernelSU module providing a transparent SSH tunnel with leak protection, TPROXY support, and an optional VPN Chain (OpenVPN over SSH).

## Features

- **Transparent TCP Proxy** — All device TCP traffic routed through SSH tunnel automatically (TPROXY or REDIRECT mode)
- **TPROXY Mode (v1.2.0+)** — mangle-table TPROXY with IPv4 + IPv6 support; auto-detected at runtime
- **Leak Protection** — IPv6 lockdown, QUIC block, conntrack flush, sysctl hardening (all toggleable)
- **SSH Tunnel** — Multiplexed SSH connection pool with payload injection for zero-rated exploitation
- **SOCKS5 Proxy** — Local `127.0.0.1:1080` for apps that support explicit proxy
- **VPN Chain** — Route traffic through Windscribe OpenVPN, tunneled inside the SSH connection
- **WebUI Dashboard** — Full control from `http://127.0.0.1:9190` (settings, profiles, runtime stats)
- **Hotspot Support** — Tethered clients (Wi-Fi, USB, Bluetooth) share the tunnel automatically

## How It Works

```
Apps TCP
  -> iptables (TPROXY or REDIRECT)
  -> sshcustomd (port 10810, transparent proxy)
  -> SSH tunnel (multiplexed, payload-injected)
  -> Internet

Optional VPN Chain:
  Apps -> tun0 (OpenVPN) -> SOCKS5 (SSH tunnel) -> Windscribe -> Internet
```

## Proxy Modes

| Mode | Table | IPv4 | IPv6 | How destination is read |
|------|-------|------|------|------------------------|
| **TPROXY** | mangle | Yes | Yes | `conn.LocalAddr()` (IP_TRANSPARENT socket) |
| **REDIRECT** | nat | Yes | No | `SO_ORIGINAL_DST` syscall |
| **Auto** (default) | — | — | — | Probes kernel; uses TPROXY if available, else REDIRECT |

Set from WebUI → Settings → Proxy Mode, or in `config.json`:
```json
"transparent_proxy": {
  "mode": "auto"
}
```

## Leak Protection

All enabled by default. Toggleable from WebUI → Settings → Leak Protection:

| Toggle | What it does |
|--------|-------------|
| Block IPv6 Leaks | `ip6tables` REJECT all outbound v6 except UID 0; apps fall back to v4 instantly |
| Block QUIC | `iptables` REJECT UDP/443+80 except UID 0; forces TCP fallback in ~50ms |
| Flush Conntrack | Drops stale flows on rule install/remove so old connections reconnect via tunnel |
| Sysctl Hardening | `route_localnet=1` + `rp_filter=2` for reliable REDIRECT-through-loopback |

## VPN Chain

Chains OpenVPN (Windscribe) through the SSH tunnel:

### Usage

```sh
vpnchain start turkey        # Start VPN with a location
vpnchain stop                # Stop, return to SSH-only
vpnchain switch netherlands  # Switch exit without full restart
vpnchain status              # Check status
vpnchain locations           # List available .ovpn configs
```

### Setup

1. Drop `.ovpn` TCP configs into `/data/adb/sshcustom-vpnchain/vpnchain/configs/`
2. Name them by location: `turkey.ovpn`, `netherlands.ovpn`, etc.
3. Auth credentials go in `/data/adb/sshcustom-vpnchain/vpnchain/auth.txt`

## Install

Flash the ZIP via Magisk or KernelSU module manager. Reboot.  
Dashboard: `http://127.0.0.1:9190/`  
Working directory: `/data/adb/sshcustom-vpnchain/`

## File Layout

```
/data/adb/sshcustom-vpnchain/
  config.json          # Main config
  profiles.json        # SSH profiles (credentials, payloads)
  bin/
    sshcustomd         # Go daemon (arm64, static)
    tun2socks          # tun2socks binary
    openvpn            # OpenVPN binary
  run/                 # PID files, logs, runtime state
  webroot/             # WebUI files
  vpnchain/
    vpnchain.sh        # VPN Chain orchestrator
    auth.txt           # Windscribe credentials
    configs/           # .ovpn files
```

## Architecture

- `sshcustomd` — Go daemon: SSH pool, SOCKS5, transparent proxy (TPROXY/REDIRECT), WebUI, API, iptables management
- `openvpn` — Static arm64 binary (OpenVPN 2.6.12, musl, OpenSSL 3.3.2)
- `tun2socks` — Backup routing tool (VPN Chain alternate path)
- `vpnchain.sh` — Shell script orchestrating OpenVPN + iptables routing

## Target

ARM64 Android devices. Tested on Poco F6 (peridot), HyperOS, kernel 6.1.x.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for full release history.

## License

See [LICENSE](LICENSE).
