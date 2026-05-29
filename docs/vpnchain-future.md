# VPN Chain — Future Implementation Reference
## Handoff Document for v2.x Implementation

> **Status:** NOT implemented in v2.0.0. This document is the complete architectural
> handoff so any developer can implement VPN Chain from scratch using only this file.

---

## 1. What VPN Chain Does

VPN Chain routes **OpenVPN (Windscribe)** through the SSHCustom SSH tunnel. The device
has no direct mobile data — only the SSH tunnel works. VPN Chain lets the user get a
different country exit IP by chaining: OpenVPN → SOCKS5 (SSH tunnel) → VPS → Windscribe.

**Use case:** User has SSH tunnel active. Wants Windscribe IP (e.g., Turkey) for a VM app.
Starts VPN Chain. Apps now exit at Windscribe's Turkey server. Stops VPN Chain to return
to normal SSH tunnel exit IP.

**This is NOT a default-on feature.** User manually starts/stops it.

---

## 2. Traffic Flow

```
Without VPN Chain (normal):
  Apps → ssh.iptables REDIRECT/TPROXY → sshcustomd → SSH tunnel → VPS → Internet

With VPN Chain:
  Apps (UID≥10000) → iptables fwmark 0x1 → policy table 200 → tun0 (OpenVPN)
  → openvpn --socks-proxy 127.0.0.1:1080 → sshcustomd SOCKS5 → SSH tunnel
  → VPS → Windscribe TCP:443 → Windscribe server → Internet

Root processes (UID=0, SSHCustom, OpenVPN itself):
  → Original route (NOT through VPN Chain) — prevents routing loop
```

**Key insight:** OpenVPN supports `--socks-proxy` natively. No tun2socks needed for the
primary flow. OpenVPN connects to Windscribe server via SOCKS5 (the SSH tunnel), creates
tun0, and pushes routes. We then use iptables fwmark to steer app traffic into tun0.

---

## 3. Components Required

### 3.1 Binaries (pre-compiled static ARM64)

**`openvpn`** — OpenVPN 2.6.x, statically linked, musl libc, OpenSSL 3.x
- Must support: `--socks-proxy`, `--route-noexec`, `--script-security 0`, `--writepid`, `--dev tun0`
- Source: compile from https://openvpn.net/community-downloads/ with Android NDK + musl
- Alternatively: extract from Alpine Linux ARM64 openvpn package

**`tun2socks`** — xjasonlyu/tun2socks, Go project, compile for linux/arm64
- Used as fallback routing layer if needed (not primary in current design)
- `go build GOOS=linux GOARCH=arm64 CGO_ENABLED=0`
- Source: https://github.com/xjasonlyu/tun2socks

### 3.2 Config Files (user-provided)

**`.ovpn` files** — Windscribe OpenVPN TCP configs
- Location: `/data/adb/sshcustom/vpnchain/configs/<location>.ovpn`
- Examples: `turkey.ovpn`, `netherlands.ovpn`, `us-east.ovpn`
- MUST use TCP protocol (not UDP) — UDP cannot traverse SOCKS5/SSH tunnel
- User generates from: https://windscribe.com/getconfig → OpenVPN TCP → choose location
- File naming convention: lowercase location name, no spaces

**`auth.txt`** — Windscribe credentials
- Location: `/data/adb/sshcustom/vpnchain/auth.txt`
- Format: line 1 = username, line 2 = password
- Permissions: `chmod 600`
- ⚠️ **NEVER commit real credentials to the repository**

### 3.3 Runtime Files (auto-created)

```
/data/adb/sshcustom/vpnchain/
  run/
    vpnchain.log          # Combined log
    openvpn.log           # OpenVPN verbose log (--log-append)
    openvpn_stdout.log    # OpenVPN stdout/stderr capture
    openvpn.pid           # OpenVPN PID (written by --writepid)
    tun2socks.pid         # tun2socks PID (if used)
    current.ovpn          # Patched .ovpn with resolved IP (avoids DNS during routing)
    state.json            # Current state: {running, location, ip}
```

---

## 4. Shell Script: `vpnchain.sh`

### 4.1 File Location
- Module: `module/vpnchain/vpnchain.sh`
- Installed to: `/data/adb/sshcustom/vpnchain.sh`
- Symlinked or copied to: `/data/adb/sshcustom/bin/vpnchain` for PATH access

### 4.2 Command Interface

```sh
vpnchain start <location>    # Start OpenVPN with <location>.ovpn
vpnchain stop                # Kill OpenVPN, remove iptables rules, restore routing
vpnchain switch <location>   # Quick switch: stop OpenVPN only, restart with new config (~5-8s)
vpnchain status              # Print JSON state: {running, location, ip}
vpnchain locations           # List available .ovpn configs (ls configs/*.ovpn | sed s/.ovpn//)
```

### 4.3 Start Sequence (`do_start`)

```
1. Check prerequisites: openvpn binary exists, auth.txt exists, .ovpn file exists
2. Check not already running (tun2socks.pid or openvpn.pid alive)
3. PRE-RESOLVE VPN server hostname NOW (before any routing change)
   - grep "^remote " from .ovpn → extract hostname
   - getent hosts $HOST || nslookup $HOST || ping -c1 $HOST
   - Save as RESOLVED_REMOTE_IP
   - If resolution fails: error out (connectivity check)
4. Patch .ovpn: replace "remote <hostname>" with "remote <RESOLVED_IP>"
   - Also remove any "tmp-dir" directive (we supply our own)
   - Write to run/current.ovpn
5. Start OpenVPN:
   openvpn \
     --config run/current.ovpn \
     --auth-user-pass auth.txt \
     --socks-proxy 127.0.0.1 1080 \
     --dev tun0 \
     --dev-type tun \
     --route-noexec \        # Disable OpenVPN's own route management
     --script-security 0 \   # No scripts (Android restriction)
     --tmp-dir run/ \
     --log-append run/openvpn.log \
     --writepid run/openvpn.pid \
     --connect-retry 5 \
     --connect-timeout 30 \
     --resolv-retry 3 \
     --verb 3
6. Wait for tun0 to appear (max 30s, check every 1s)
   - If openvpn exits during wait: capture logs, error out
7. Configure iptables routing (see Section 5)
8. Write state.json: {running: true, location: $LOCATION, ip: $REMOTE_IP}
```

### 4.4 Stop Sequence (`do_stop`)

```
1. Remove iptables rules (in reverse order of addition):
   iptables -t mangle -D OUTPUT ... fwmark 0x1 (app TCP mark)
   iptables -t mangle -D OUTPUT ... fwmark 0x1 (DNS mark)
   iptables -t nat -D OUTPUT ... uid 10000-99999 RETURN (proxy bypass)
   iptables -t nat -D POSTROUTING -o tun0 MASQUERADE
2. Remove policy routing:
   ip rule del fwmark 0x1 table 200
   ip route flush table 200
3. Kill OpenVPN:
   kill_pid_file run/openvpn.pid
   killall openvpn (safety net)
4. Bring down tun0:
   ip link set tun0 down
5. Kill tun2socks if used:
   kill_pid_file run/tun2socks.pid
   killall tun2socks
6. Cleanup TUN interface (if tun2socks was used):
   ip link del tun1
7. Write state.json: {running: false, location: "", ip: ""}
```

### 4.5 Switch Sequence (`do_switch`)

```
1. Pre-resolve new location's hostname (same as do_start step 3)
2. Remove iptables routing rules (same as do_stop steps 1-2)
3. Kill ONLY OpenVPN (keep tun2socks if it was running)
   kill_pid_file run/openvpn.pid
   killall openvpn
4. Bring down tun0: ip link set tun0 down
5. sleep 1 (allow interface teardown)
6. Start OpenVPN with new config (same as do_start steps 4-7)
7. Update state.json with new location
Total downtime: ~5-8 seconds
```

---

## 5. iptables Routing for VPN Chain

**Goal:** App traffic (UID 10000-99999) exits via tun0 (Windscribe IP).
Root traffic (UID 0 = SSHCustom daemon, OpenVPN itself) stays on original route (prevents loop).

```sh
# Create routing table 200: all traffic → tun0
ip route flush table 200
ip route add default dev tun0 table 200

# Policy rule: packets marked 0x1 use table 200
ip rule del fwmark 0x1 table 200 2>/dev/null
ip rule add fwmark 0x1 table 200 priority 100

# Mark OUTPUT packets from apps (UID 10000-99999) with 0x1
iptables -t mangle -A OUTPUT \
  -m owner --uid-owner 10000-99999 \
  -j MARK --set-mark 0x1

# Mark DNS queries from apps too (so they use Windscribe DNS via tun0)
iptables -t mangle -A OUTPUT \
  -p udp --dport 53 \
  -m owner --uid-owner 10000-99999 \
  -j MARK --set-mark 0x1

# Bypass ssh.iptables transparent proxy for app traffic
# ssh.iptables uses nat OUTPUT REDIRECT to intercept TCP.
# Insert RETURN rule at TOP so apps skip the redirect and fall through to fwmark routing.
iptables -t nat -I OUTPUT 1 \
  -m owner --uid-owner 10000-99999 \
  -j RETURN

# NAT masquerade for traffic going out tun0
iptables -t nat -A POSTROUTING -o tun0 -j MASQUERADE
```

**Why this works:**
- Root (UID 0): not marked → uses main routing table → goes through SSH tunnel → no loop
- Apps (UID 10000+): marked 0x1 → table 200 → tun0 → OpenVPN → SOCKS5:1080 → SSH tunnel → Windscribe

**Chain naming for v2.0:** When implementing in v2.0, create named chains with `SSHC_VC_`
prefix for all VPN Chain rules to enable targeted cleanup:
```
SSHC_VC_MANGLE_OUT   — app TCP/UDP mark rules
SSHC_VC_NAT_BYPASS   — proxy bypass for apps
SSHC_VC_NAT_POSTROUTE — MASQUERADE
```

---

## 6. Integration with `settings.ini`

Add the following keys to `settings.ini` for VPN Chain support:

```ini
# VPN Chain
vpnchain_enabled="false"        # Whether VPN Chain feature is enabled at all
vpnchain_socks_port="1080"      # SOCKS5 port provided by SSHCustom (default 1080)
vpnchain_table="200"            # Policy routing table number for VPN Chain
vpnchain_fwmark="0x1"           # fwmark value for app traffic routing
vpnchain_uid_range="10000-99999" # UID range for app traffic
vpnchain_udpgw_server=""        # Optional udpgw server for UDP over VPN Chain
```

---

## 7. Integration with `ssh.iptables`

Add a VPN Chain mode check to `ssh.iptables enable`:

```sh
# After main iptables rules applied, if VPN Chain is active:
if [ -f "$vpnchain_run_dir/openvpn.pid" ] && kill -0 "$(cat $vpnchain_run_dir/openvpn.pid)" 2>/dev/null; then
  log Info "VPN Chain active — reapplying routing rules after iptables renew"
  "$vpnchain_script" _reapply_routing
fi
```

Add to `ssh.iptables disable` / cleanup:

```sh
# Always clean up VPN Chain rules on full cleanup
# (handles case where ssh.iptables disable is called while vpnchain is running)
vpnchain_cleanup() {
  iptables -t mangle -D OUTPUT -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t mangle -D OUTPUT -p udp --dport 53 -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t nat -D OUTPUT -m owner --uid-owner 10000-99999 -j RETURN 2>/dev/null
  iptables -t nat -D POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null
  ip rule del fwmark 0x1 table 200 2>/dev/null
  ip route flush table 200 2>/dev/null
}
```

**Critical:** `ssh.iptables disable` (i.e., `ssh.service stop`) MUST clean up VPN Chain
rules even if `vpnchain stop` was not called first. The `vpnchain_cleanup()` function
above should be called in `cleanup_iptables()` in `ssh.iptables`.

---

## 8. Go Daemon API Endpoints

Add to `daemon/cmd/sshcustomd/main.go` (or a new `internal/vpnchain/` package):

```
POST /api/v1/vpnchain/start
  Body: {"location": "turkey"}
  Response: {"ok": true, "data": {"location": "turkey", "ip": "..."}
           | {"ok": false, "error": "message"}

POST /api/v1/vpnchain/stop
  Body: {}
  Response: {"ok": true, "data": {}}

POST /api/v1/vpnchain/switch
  Body: {"location": "netherlands"}
  Response: {"ok": true, "data": {"location": "netherlands", "ip": "..."}

GET /api/v1/vpnchain/status
  Response: {"ok": true, "data": {"running": true, "location": "turkey", "ip": "1.2.3.4"}}

GET /api/v1/vpnchain/locations
  Response: {"ok": true, "data": {"locations": ["turkey", "netherlands", "us-east"]}}
```

**Implementation:** Each endpoint shells out to `vpnchain.sh` via `exec.Command`:
```go
func vpnchainExec(args ...string) (string, error) {
    cmd := exec.Command("/data/adb/sshcustom/vpnchain.sh", args...)
    out, err := cmd.CombinedOutput()
    return string(out), err
}
```

State is read from `run/state.json` (written by vpnchain.sh).

---

## 9. Android App UI

**Home screen addition:** When VPN Chain is running, show a "VPN Chain" status card:
```
┌─────────────────────────┐
│ 🔗 VPN Chain            │
│ Turkey · 1.2.3.4        │
│ [Switch]  [Stop]        │
└─────────────────────────┘
```

**Settings screen addition:** VPN Chain card:
```
VPN Chain
  SOCKS5 Port: [1080]
  [Open configs folder]    → opens file manager at configs dir
  [Check auth.txt]         → shows whether auth.txt exists (not contents)
```

**Profiles screen addition:** VPN Chain Locations list (read from GET /api/v1/vpnchain/locations)
- Tap to start/switch to that location
- "Add Location" button → instructions to drop .ovpn in configs dir

---

## 10. Observed Quirks and Pitfalls

### DNS Resolution Must Happen Before tun2socks/OpenVPN Starts
When tun2socks is running (tun1 up) or when OpenVPN has changed the routing, DNS queries
may fail or route through the wrong interface. Always resolve the VPN server hostname
**before** launching any VPN Chain component. Pre-resolution technique:
```sh
REMOTE_HOST=$(grep '^remote ' config.ovpn | head -1 | awk '{print $2}')
RESOLVED_IP=$(getent hosts $REMOTE_HOST | awk '{print $1}') \
  || RESOLVED_IP=$(nslookup $REMOTE_HOST | awk '/^Address: /{print $2; exit}') \
  || RESOLVED_IP=$(ping -c1 -W2 $REMOTE_HOST | head -1 | grep -oE '[0-9.]+')
# Patch config: replace "remote $REMOTE_HOST" with "remote $RESOLVED_IP"
```

### Android Has No `/tmp`
OpenVPN requires a writable temp directory. Create `/tmp` explicitly or pass `--tmp-dir $RUN_DIR`.

### OpenVPN `--writepid` Timing
OpenVPN writes its PID file after init, not immediately. The shell needs to `sleep 2` and
then fall back to the nohup `$!` value if the PID file hasn't appeared yet.

### iptables nat `OUTPUT` Chain Ordering
`ssh.iptables` inserts `SSHC_OUTPUT` into nat OUTPUT. VPN Chain's bypass rule must be
inserted at position 1 (before SSHC_OUTPUT) using `-I OUTPUT 1`, not `-A OUTPUT`.
Otherwise apps' TCP packets hit SSHC_OUTPUT first and get redirected to the transparent
proxy port instead of using fwmark routing.

### `--route-noexec` is Mandatory
OpenVPN would normally push routes and replace the default gateway, which would break the
SSH tunnel (which IS the route to the VPN server). `--route-noexec` prevents OpenVPN from
modifying the routing table. We manage routes manually via iptables fwmark.

### `--script-security 0` is Mandatory
Android denies execution of scripts pushed by the VPN server. `--script-security 0`
prevents OpenVPN from trying to execute up/down scripts.

### tun0 May Already Exist
On devices with other VPN apps or after a failed stop, `tun0` may already exist.
Add `ip link set tun0 down 2>/dev/null; ip link del tun0 2>/dev/null` before starting OpenVPN.

### Windscribe `.ovpn` Must Use TCP
The SSH tunnel is TCP-based. OpenVPN over SOCKS5 only works with TCP protocol.
UDP OpenVPN configs will fail. User must download "OpenVPN TCP" configs from Windscribe
config generator. UDP configs can be detected: `grep '^proto udp' config.ovpn`.

### switch vs stop+start
`switch` is ~5-8s (kills only OpenVPN, keeps tun2socks). `stop+start` is ~15-20s (full
teardown + restart). Prefer `switch` for location changes.

---

## 11. auth.txt Security

`auth.txt` contains plaintext Windscribe credentials. In the repository:
- The file at `module/vpnchain/auth.txt` should contain ONLY placeholder text
- `.gitignore` should exclude `module/vpnchain/auth.txt` with real credentials
- The user copies their real `auth.txt` to `/data/adb/sshcustom/vpnchain/auth.txt` after install
- File permissions must be `600` (root-only read)

---

## 12. Future Enhancement: tun2socks Mode

The current design uses OpenVPN's native `--socks-proxy` (simpler, fewer processes).
A future tun2socks mode would be:

```
Apps → tun1 (tun2socks --proxy socks5://127.0.0.1:1080) → SSH tunnel → Internet
```

This would enable non-OpenVPN VPN protocols (WireGuard, etc.) via the SSH tunnel.
tun2socks binary is already in the module (`module/bin/tun2socks`). The vpnchain.sh
already has `start_tun2socks()` implemented (though unused in primary flow).

For WireGuard over SSH tunnel:
```
wg-quick up → wg0 (WireGuard) → AllowedIPs = 0.0.0.0/0 except SSH server
→ tun2socks on tun1 provides SOCKS5 upstream
→ WireGuard routes through tun1 → SSH tunnel
```

---

## 13. Prerequisites Checklist for Implementation

Before implementing VPN Chain in v2.x:

- [ ] `module/bin/openvpn` — static arm64 binary present
- [ ] `module/bin/tun2socks` — static arm64 binary present (already done)
- [ ] `module/vpnchain/vpnchain.sh` — controller script
- [ ] `module/vpnchain/configs/.gitkeep` — placeholder for user .ovpn files
- [ ] `module/vpnchain/auth.txt` — placeholder only (gitignored for real credentials)
- [ ] `customize.sh` — copies `vpnchain/` directory to `/data/adb/sshcustom/vpnchain/`
- [ ] `settings.ini` — add vpnchain_* keys
- [ ] `ssh.iptables` — add `vpnchain_cleanup()` to main cleanup function
- [ ] `daemon/` — add `/api/v1/vpnchain/*` endpoints
- [ ] Android app — Home VPN Chain card + Profiles locations list
- [ ] CI — no new build steps needed (tun2socks already built; openvpn is pre-compiled)
