# SSHCustom-VPNChain

A Magisk/KernelSU/APatch module that tunnels all device traffic through an SSH connection with optional OpenVPN chaining. Designed for Android arm64 devices with root access.

## Features

- **SSH Tunnel Modes** — Redirect (NAT, TCP), TPROXY (kernel transparent proxy, TCP+UDP), TUN/TUN+UDPGW
- **Payload Injection** — Custom HTTP payloads for carrier bypass (HTTP Injector / HTTP Custom compatible)
- **SNI Spoofing** — Direct SNI or SNI-over-HTTP-proxy transport modes
- **DNS Through Tunnel** — Local DNS forwarder (UDP 127.0.0.1:5353) that resolves all DNS queries as TCP through the SSH tunnel, fixing "no internet" in YouTube/Chrome
- **VPN Chain** — Route OpenVPN over the SSH tunnel (OpenVPN over SSH SOCKS5)
- **Hotspot Sharing** — Forward hotspot/tethering clients through the tunnel
- **Transparent Reconnect** — SSH reconnects without flapping iptables; apps stall briefly with no traffic leak (fail-closed)
- **Latency Measurement** — In-app TCP connect latency test (Google, Cloudflare) through the SOCKS proxy
- **Channel Pool** — Pre-warmed SSH channels for reduced per-connection latency
- **TCP Buffer Tuning** — Maximize socket buffers for high throughput
- **QUIC Block** — Force TCP fallback for apps that prefer QUIC
- **IPv6 Control** — Disable IPv6 system-wide while tunnel is active
- **Module Status** — Real-time status in KSU/Magisk module manager (running/reconnecting/stopped)
- **WebUI** — Local HTTP API on 127.0.0.1:9190 for status and control
- **Companion App** — Miuix-based Material You app with profiles, logs, settings, and VPN Chain management

## Requirements

- **Root**: Magisk, KernelSU, or APatch
- **Architecture**: arm64 only
- **Android**: 8.0+ (API 26+)

## Installation

1. Download the module ZIP from the [Releases](https://github.com/GoodyOG/sshcustom-vpnchain/releases) page.
2. Flash via Magisk Manager, KSU Module, or APatch Module installer.
3. Reboot the device.
4. Install the companion APK from the same release.
5. Open the app, grant root access, configure a profile, and tap **Start**.

## Configuration

All settings are stored in `/data/adb/sshcustom/settings.ini`. The companion app writes to this file automatically. Key settings:

### SSH Connection
| Setting | Description |
|---------|-------------|
| `ssh_host` | SSH server hostname or IP |
| `ssh_port` | SSH server port (default: 22) |
| `ssh_user` | SSH username |
| `ssh_password` | SSH password |
| `ssh_mode` | Transport: `direct`, `sni`, `sni_http_proxy` |
| `ssh_sni_host` | SNI hostname for carrier bypass |
| `payload_enabled` | Enable payload injection (`true`/`false`) |
| `payload` | Raw payload string (variables: `[host]` `[port]` `[crlf]`) |

### Traffic Interception
| Setting | Description |
|---------|-------------|
| `network_mode` | `redirect` (NAT TCP) or `tproxy` (kernel, TCP+UDP) |
| `socks_port` | SOCKS5 proxy port (default: 1081) |
| `redir_port` | Redirect/transparent port (default: 9799) |
| `tproxy_port` | TPROXY port (default: 9899) |

### Proxy Behaviour
| Setting | Description |
|---------|-------------|
| `proxy_tcp` | Tunnel TCP traffic (`true`/`false`) |
| `proxy_udp` | Tunnel UDP traffic — TPROXY mode only |
| `quic` | `disable` = block QUIC, `enable` = allow |

### Performance
| Setting | Description |
|---------|-------------|
| `channel_pool` | Pre-warm SSH channels (`true`/`false`) |
| `channel_pool_size` | Number of pre-warmed channels (default: 8) |
| `tcp_buffer_tuning` | Maximize TCP buffers (`true`/`false`) |

### DNS
| Setting | Description |
|---------|-------------|
| `dns_hijack_tcp` | Hijack TCP DNS queries |
| `dns_hijack_udp` | Hijack UDP DNS queries |
| `dns_hijack_mode` | `redirect`, `tproxy`, or `disable` |

### Other
| Setting | Description |
|---------|-------------|
| `ipv6` | `false` = disable IPv6 while tunnel active |
| `hotspot_sharing` | Share tunnel with hotspot clients |

## Architecture

```
┌─────────────────────────────────────────────────────┐
│  Companion App (Kotlin/Compose + Miuix)             │
│  ├── MainActivity / MainViewModel                   │
│  ├── RootService (IPC to daemon)                    │
│  └── Screens: Home, Profiles, VPN Chain, Logs, Settings │
└──────────────────────┬──────────────────────────────┘
                       │ Unix socket / HTTP API
┌──────────────────────▼──────────────────────────────┐
│  sshcustomd (Go, static arm64 binary)               │
│  ├── SSH client (direct/SNI/proxy transport)        │
│  ├── SOCKS5 server                                  │
│  ├── Transparent/TPROXY proxy                       │
│  ├── DNS forwarder (UDP→TCP through tunnel)         │
│  ├── HTTP API (127.0.0.1:9190)                      │
│  └── Metrics (CPU, RAM, throughput, connections)    │
└──────────────────────┬──────────────────────────────┘
                       │ iptables (SSHC_* chains)
┌──────────────────────▼──────────────────────────────┐
│  Module Scripts (POSIX sh)                          │
│  ├── ssh.service — start/stop/restart daemon        │
│  ├── ssh.iptables — traffic interception rules      │
│  ├── ssh.tool — speed boost, cgroup, utilities      │
│  └── ovpn.service — VPN Chain (OpenVPN over SSH)    │
└─────────────────────────────────────────────────────┘
```

### Module Data Path

All runtime data is stored under `/data/adb/sshcustom/`:
- `bin/` — daemon binary
- `run/` — PID, logs, socket, state
- `scripts/` — service and iptables scripts
- `settings.ini` — configuration
- `vpnchain/configs/` — OpenVPN config files (.ovpn)

## Building

### Daemon (Go)
```bash
cd daemon
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build \
  -trimpath -buildvcs=false \
  -ldflags="-s -w -X main.version=2.3.0" \
  -o sshcustomd ./cmd/sshcustomd/
```

### App (Android)
```bash
cd app
./gradlew assembleRelease
```

## License

See [LICENSE](LICENSE).
