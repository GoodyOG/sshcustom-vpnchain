# SSHCustom-VPNChain

Magisk/KernelSU/APatch root module for ARM64 Android providing a system-wide SSH
transparent proxy — no VpnService required.

---

## What It Does

Routes all device TCP traffic through an SSH tunnel via iptables, entirely at the
kernel level using root. Works without an active mobile data plan by exploiting
carrier zero-rated/free-host loopholes via payload injection or SNI spoofing.

---

## Requirements

- Rooted Android 9+ (API 28+)
- Magisk v20.4+ **or** KernelSU **or** APatch
- ARM64 device only (`arm64-v8a`)

---

## Installation

1. Flash `SSHCustom-VPNChain-v*.zip` via your root manager
2. Install the companion APK
3. Reboot
4. Open the app → configure SSH credentials → **Start**

---

## SSH Modes

| Mode | Description |
|---|---|
| `direct` | Plain SSH tunnel — dial SSH server directly |
| `sni` | SSH wrapped in TLS with custom SNI hostname (carrier bypass) |
| `sni_http_proxy` | Connect through HTTP proxy first, then apply SNI-TLS wrap |

### Payload Injection

All modes support a raw payload string injected before or inside the SSH handshake.
Paste your HTTP Injector or similar payload into the app. Supported variables:
`[host]` `[port]` `[crlf]` `[cr]` `[lf]`

---

## Traffic Modes

| Mode | Description | Requires |
|---|---|---|
| `redirect` | iptables nat REDIRECT (TCP only) | Any kernel |
| `tproxy` | iptables mangle TPROXY (TCP+UDP) | TPROXY kernel modules |
| `tun` | tun2proxy TUN device | tun2proxy binary |
| `tun_udpgw` | TUN + UDP gateway server for real UDP | tun2proxy + VPS udpgw |

If `tproxy` kernel modules are missing, the module auto-downgrades to `redirect`
with a log warning.

---

## Speed Boost

**Channel Pool** — Pre-warms SSH channels in the background, eliminating
per-connection negotiation latency. On high-latency SSH connections (~200ms RTT)
this measurably improves download throughput.

**BBR** — Enables BBR congestion control if the kernel supports it.

**TCP Buffer Tuning** — Sets `net.core.rmem_max` and `tcp_wmem` to 128MB max.

All speed settings are restored to original values when the tunnel stops.

---

## QUIC Toggle

Default: **disabled** (blocks UDP 443 and UDP 80 via iptables OUTPUT DROP).
This forces all QUIC-capable apps (Chrome, YouTube, etc.) to fall back to TCP,
which the SSH tunnel can carry. Enable QUIC only if you have a UDP transport.

---

## Stop Behaviour

`ssh.service stop` guarantees a 100% clean state:
- All `SSHC_*` iptables chains removed from nat, mangle, filter tables
- All policy routing rules (`ip rule`, `ip route`) removed
- TUN device deleted
- VPN Chain routing rules cleaned (even if vpnchain stop was not called)
- IPv6 restored if it was disabled
- BBR and TCP buffer settings restored to pre-tunnel values

Any other VPN app (Windscribe, etc.) can connect immediately after stop.

---

## Configuration

Edit `/data/adb/sshcustom/settings.ini` or use the companion app:

```ini
ssh_host="your.server.com"
ssh_port="22"
ssh_user="user"
ssh_password="password"
ssh_mode="direct"          # direct | sni | sni_http_proxy
network_mode="redirect"    # redirect | tproxy | tun | tun_udpgw
quic="disable"             # disable | enable
channel_pool="true"
bbr_enabled="true"
```

---

## File Layout

```
/data/adb/sshcustom/
  settings.ini        — master config
  bin/
    sshcustomd        — Go daemon (SSH, SOCKS5, transparent proxy, HTTP API)
    tun2proxy         — TUN device proxy (tun/tun_udpgw modes)
  scripts/
    ssh.service       — lifecycle: start/stop/restart/status
    ssh.iptables      — iptables rules (all 4 modes + capability probe)
    ssh.tool          — helpers: BBR, TCP buffers, cgroup, logging
  run/
    sshcustom.log     — main log
    sshcustom.pid     — daemon PID
    state/            — runtime snapshots (iptables, capabilities)
  vpnchain/           — VPN Chain data (coming in v2.x)
    configs/          — drop .ovpn files here
    auth.txt          — VPN credentials (chmod 600)
```

---

## VPN Chain

**Coming in a future version.** See [`docs/vpnchain-future.md`](docs/vpnchain-future.md)
for the complete architecture. VPN Chain will route OpenVPN (Windscribe) through the
SSH tunnel to provide a different country exit IP.

---

## Architecture

- `sshcustomd` — statically compiled Go binary (zero dependencies, ~6MB)
  - HTTP REST API at `127.0.0.1:9190/api/v1/*`
  - Unix socket at `/data/adb/sshcustom/run/sshcustomd.sock` for app
  - SSH channel pool for high-throughput forwarding
  - tun2proxy subprocess management
- Shell scripts modeled after [boxproxy/box](https://github.com/boxproxy/box)
- Android app: Kotlin + Jetpack Compose + miuix + libsu

---

## License

Apache 2.0 — see [LICENSE](LICENSE)
