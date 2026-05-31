# SSHCustom-VPNChain

Magisk/KernelSU module that provides SSH tunneling with an optional VPN Chain feature (OpenVPN through SSH).

## Features

- **SSH Tunnel** — SOCKS5 proxy + transparent TCP proxy via SSH with payload injection
- **VPN Chain** — Route app traffic through Windscribe OpenVPN, tunneled inside the SSH connection
- **WebUI** — Control everything from your browser at `http://127.0.0.1:9190`
- **Per-app routing** — Only apps go through VPN; SSH tunnel stays untouched on mobile data

## VPN Chain

Chains OpenVPN (Windscribe) through the SSH tunnel:

```
Apps → tun0 (OpenVPN) → SOCKS5 (SSH tunnel) → VPS → Windscribe server → Internet
```

SSHCustom stays connected as the transport layer. VPN Chain is a toggle — turn it on when you need a different exit IP, turn it off to go back to normal SSH tunnel.

### Usage

```sh
# Start VPN with a location
vpnchain start turkey

# Stop and return to normal SSH
vpnchain stop

# Switch location without full restart
vpnchain switch netherlands

# Check status
vpnchain status

# List available locations
vpnchain locations
```

### Setup

1. Drop `.ovpn` TCP configs into `/data/adb/sshcustom/vpnchain/configs/`
2. Name them by location: `turkey.ovpn`, `netherlands.ovpn`, etc.
3. Auth credentials go in `/data/adb/sshcustom/vpnchain/auth.txt`

## Install

Flash the ZIP via Magisk or KernelSU module manager. Reboot.

## Architecture

- `sshcustomd` — Go daemon handling SSH connections, SOCKS5, transparent proxy, WebUI
- `openvpn` — Static arm64 binary (OpenVPN 2.6.12, musl, OpenSSL 3.3.2)
- `tun2socks` — Backup routing tool (not used in current VPN Chain flow)
- `vpnchain.sh` — Shell script orchestrating OpenVPN + iptables routing

## Target

ARM64 Android devices only.
