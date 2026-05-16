# SSHCustom-VPNChain — Session Context

## IMPORTANT: Token & Workflow
The user will paste their GitHub personal access token in the chat. **Always use it directly** for all git operations — pushing, tagging, creating releases, API calls. Never ask the user to do anything manually. Do everything autonomously: code, build, test, commit, push, tag, release.

Token usage pattern:
```
git push https://<TOKEN>@github.com/GoodyOG/sshcustom-vpnchain.git
curl -H "Authorization: token <TOKEN>" https://api.github.com/...
```

## Repository
- **Repo:** GoodyOG/sshcustom-vpnchain
- **Base code:** Full mirror of GoodyOG/SSHCustom-Magisk v2.2.1 (already pushed)
- **Purpose:** Personal single Magisk/KSU module combining SSHCustom + VPN Chain (Windscribe over SSH tunnel)
- **Audience:** Only the repo owner (GoodyOG). Not a public product. No need to worry about other users.

## What SSHCustom-Magisk already does (v2.2.1)
- Go daemon (`sshcustomd`) starts at boot in idle mode, WebUI at 127.0.0.1:9190
- SSH tunnel connects via payload injection/SNI tricks to bypass ISP on **zero-data** (free host exploit, no active data plan)
- Transparent TCP proxy via iptables REDIRECT (all device TCP goes through SSH)
- SOCKS5 proxy at 127.0.0.1:1080
- Tunnel start/stop/restart from WebUI and API
- ARM64 + ARM dual-ABI builds via GitHub Actions
- module.prop status with emoji indicators
- WebUI with Home, Profiles, Runtime, Settings tabs

## The User's Situation
1. Has **no active mobile data plan** — only the SSH tunnel works (payload injection exploits a zero-rated/free host loophole)
2. Is a **Windscribe VPN premium subscriber** — wants to use different country IPs occasionally
3. Uses a VM app (Virtual Master) on Android where they do things requiring a Windscribe IP
4. **Problem:** Windscribe Android app uses VpnService which conflicts with SSHCustom iptables and cant connect because no direct data exists
5. **Solution needed:** Route Windscribe VPN connection THROUGH the SSH tunnel so it works without direct data

## What needs to be added: VPN Chain feature

### How it works
When the user wants a Windscribe IP:
1. SSHCustom is already connected (SOCKS5 at 127.0.0.1:1080 active)
2. User triggers "vpnchain start" (from WebUI or shell)
3. `tun2socks` starts - creates a TUN interface that routes through SOCKS5 (SSH tunnel)
4. `openvpn` starts - connects to Windscribe via TCP 443 routed through tun2socks which goes through SSH tunnel
5. OpenVPN creates its own TUN and device traffic exits at Windscribe IP
6. User does their thing in the VM
7. User triggers "vpnchain stop" and everything tears down back to normal SSHCustom

Traffic flow:
```
Apps -> OpenVPN tun0 (Windscribe IP) -> tun2socks tun1 (SOCKS5) -> SSH tunnel -> SSH server -> Windscribe TCP 443 -> Internet
```

### This is NOT a default/always-on feature
- User manually starts it when needed
- Uses it for a while
- Stops it and goes back to normal SSHCustom
- No boot integration for vpnchain

### Components to add
1. **tun2socks binary (ARM64 + ARM)** — cross-compile from github.com/xjasonlyu/tun2socks for linux/arm64 and linux/arm
2. **openvpn static binary (ARM64 + ARM)** — get or cross-compile for Android
3. **vpnchain.sh script** — shell commands: start, stop, switch, status
4. **Windscribe .ovpn TCP configs directory** — user downloads from windscribe.com/getconfig
5. **auth.txt** — Windscribe credentials file (username line 1, password line 2)
6. **WebUI integration** — section in the existing WebUI for VPN Chain control
7. **Go daemon API endpoints** — for WebUI to call

### File structure (additions)
```
src/module/vpnchain/
  bin/
    tun2socks-arm64
    tun2socks-arm
    openvpn-arm64
    openvpn-arm
  vpnchain.sh

/data/adb/sshcustom/vpnchain/     (created at install or first use)
  configs/
    us-east.ovpn
    uk.ovpn
    ... (user adds configs here)
  auth.txt
  run/                             (runtime: pid files, logs)
```

### vpnchain.sh commands
```sh
vpnchain start <location>    # Start tun2socks + openvpn with <location>.ovpn
vpnchain stop                # Kill everything, restore normal routing
vpnchain switch <location>   # Quick switch: kill openvpn, restart with new config (~5-8s)
vpnchain status              # Print: running/stopped, location, Windscribe IP
```

### Go daemon API endpoints to add
```
POST /api/v1/vpnchain/start    body: {"location": "us-east"}
POST /api/v1/vpnchain/stop
POST /api/v1/vpnchain/switch   body: {"location": "uk"}
GET  /api/v1/vpnchain/status   returns {"running": true, "location": "us-east", "ip": "..."}
GET  /api/v1/vpnchain/locations returns ["us-east", "uk", "germany", ...]
```

### WebUI additions
Add a "VPN Chain" section (could be a new tab or section in Settings):
- Toggle / Start button with location dropdown
- Current status display (connected location + IP)
- Switch location dropdown + button
- Stop button

### Key technical notes
- SSHCustom SOCKS5 must be enabled in config.json (socks_enabled: true, socks_port: 1080)
- tun2socks and openvpn run as root (Magisk module context)
- No Android VpnService used — TUN interfaces created directly via /dev/net/tun with root
- This avoids the "only one VPN at a time" Android limitation
- Windscribe .ovpn configs MUST use TCP protocol (port 443) — UDP wont work through SOCKS/SSH tunnel
- User generates configs from windscribe.com My Account VPN Config Generator OpenVPN TCP
- Country switching = kill openvpn + restart with different .ovpn = ~5-8 seconds

### Build requirements
- Cross-compile tun2socks (Go project, github.com/xjasonlyu/tun2socks) for GOOS=linux GOARCH=arm64 and GOARCH=arm
- Get or compile static openvpn binary for arm64/arm
- Update build.sh / CI workflow to include vpnchain binaries in the final module ZIP
- The module ZIP should contain everything — one flash installs SSHCustom + VPN Chain

### Implementation steps
1. Cross-compile tun2socks for arm64/arm, add to repo
2. Source or compile static openvpn for arm64/arm, add to repo
3. Write vpnchain.sh (the core logic script)
4. Add Go daemon API endpoints for vpnchain control (calls vpnchain.sh internally)
5. Add WebUI section for VPN Chain
6. Update module packaging (customize.sh copies vpnchain files)
7. Test build compiles
8. Commit, push, tag, release

### Windscribe info
- Download page: https://windscribe.com/download?platform=desktop&os=linux&cpid=feat-linux
- They offer Linux CLI ARM64 but we DONT use windscribe-cli
- We use raw openvpn + their .ovpn configs generated from their website
- The Windscribe Android app CANNOT be used because VpnService conflicts and there is no direct data

### Reference project (concept only)
https://github.com/GAME-OVER-op/ZDT-D — Root Android network orchestration module using tun2socks + netd binding + per-app routing. We took the concept of tun2socks over SOCKS5 from here but building much simpler.
