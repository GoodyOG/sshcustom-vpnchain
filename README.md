# SSHCustom-VPNChain

Personal Magisk/KSU module: SSH tunnel + Windscribe VPN chain routing for rooted Android.

[![Build](https://github.com/GoodyOG/sshcustom-vpnchain/actions/workflows/build.yml/badge.svg)](https://github.com/GoodyOG/sshcustom-vpnchain/actions/workflows/build.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/GoodyOG/sshcustom-vpnchain?sort=semver)](https://github.com/GoodyOG/sshcustom-vpnchain/releases/latest)

## What this does

Based on [SSHCustom-Magisk](https://github.com/GoodyOG/SSHCustom-Magisk) v2.2.1 with an added **VPN Chain** feature:

```
Apps → OpenVPN tun0 (Windscribe IP) → tun2socks tun1 (SOCKS5) → SSH tunnel → Windscribe server → Internet
```

- SSH tunnel provides zero-data connectivity (payload injection)
- VPN Chain routes Windscribe OpenVPN through the SSH tunnel's SOCKS5 proxy
- Switch Windscribe locations on the fly (~5-8s)
- No VpnService needed — TUN interfaces created directly as root

## Features (inherited from SSHCustom-Magisk)

- SSH connection pool (4 parallel sessions)
- SOCKS5 proxy + transparent TCP via iptables
- Pluggable transport (direct, HTTP proxy, TLS/SNI, payload injection)
- Hotspot tethering
- WebUI dashboard at `http://127.0.0.1:9190/`
- Autostart on boot

## VPN Chain usage

1. Flash the module ZIP, reboot
2. Start SSHCustom tunnel as normal
3. Place `.ovpn` files (TCP 443) in `/data/adb/sshcustom/vpnchain/configs/`
4. Open WebUI → VPN Chain tab → select location → Start

Or from shell:
```sh
vpnchain start turkey
vpnchain switch germany
vpnchain stop
vpnchain status
```

## Build

Requires Go 1.23+ and Python 3:

```bash
./build.sh
```

## License

[Apache License 2.0](LICENSE)
