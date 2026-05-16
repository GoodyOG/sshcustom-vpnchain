#!/system/bin/sh
# vpnchain.sh — VPN Chain controller for SSHCustom-VPNChain
# Routes OpenVPN (Windscribe) through SSHCustom's SOCKS5 tunnel via tun2socks.
#
# Usage:
#   vpnchain start <location>   Start tun2socks + openvpn with <location>.ovpn
#   vpnchain stop               Kill everything, restore normal routing
#   vpnchain switch <location>  Quick switch: kill openvpn, restart with new config
#   vpnchain status             Print current state as JSON

set -u

WORK_DIR="/data/adb/sshcustom"
VPNCHAIN_DIR="$WORK_DIR/vpnchain"
CONFIGS_DIR="$VPNCHAIN_DIR/configs"
AUTH_FILE="$VPNCHAIN_DIR/auth.txt"
RUN_DIR="$VPNCHAIN_DIR/run"
LOG_FILE="$RUN_DIR/vpnchain.log"
TUN2SOCKS_PID="$RUN_DIR/tun2socks.pid"
OPENVPN_PID="$RUN_DIR/openvpn.pid"
STATE_FILE="$RUN_DIR/state.json"
BIN_DIR="$WORK_DIR/bin"
TUN2SOCKS_BIN="$BIN_DIR/tun2socks"
OPENVPN_BIN="$BIN_DIR/openvpn"

# tun2socks interface
TUN2SOCKS_TUN="tun1"
TUN2SOCKS_ADDR="10.255.0.1/24"
TUN2SOCKS_GW="10.255.0.1"
# SOCKS5 provided by SSHCustom
SOCKS5_ADDR="127.0.0.1:1080"

mkdir -p "$RUN_DIR" "$CONFIGS_DIR"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$LOG_FILE"; }

pid_alive() { [ -n "$1" ] && kill -0 "$1" 2>/dev/null; }

read_pid() {
  [ -f "$1" ] || return 1
  PID="$(cat "$1" 2>/dev/null)"
  pid_alive "$PID" && echo "$PID" && return 0
  return 1
}

kill_pid_file() {
  FILE="$1"; NAME="$2"
  if [ -f "$FILE" ]; then
    PID="$(cat "$FILE" 2>/dev/null)"
    if pid_alive "$PID"; then
      log "stopping $NAME pid=$PID"
      kill -TERM "$PID" 2>/dev/null
      for i in 1 2 3 4 5; do pid_alive "$PID" || break; sleep 1; done
      pid_alive "$PID" && kill -KILL "$PID" 2>/dev/null
    fi
    rm -f "$FILE"
  fi
}

write_state() {
  # $1=running (true/false), $2=location, $3=ip (optional)
  cat > "$STATE_FILE" <<EOF
{"running": $1, "location": "$2", "ip": "$3"}
EOF
}

get_vpn_ip() {
  # Try to get the IP assigned to tun0 (OpenVPN's interface)
  IP="$(ip -4 addr show tun0 2>/dev/null | grep -oE 'inet [0-9.]+' | awk '{print $2}')"
  [ -n "$IP" ] && echo "$IP" && return 0
  echo ""
}

cleanup_tun() {
  # Remove tun2socks interface if it exists
  ip link set "$TUN2SOCKS_TUN" down 2>/dev/null
  ip tun del "$TUN2SOCKS_TUN" 2>/dev/null || ip link del "$TUN2SOCKS_TUN" 2>/dev/null
}

cleanup_routes() {
  # Remove any routes we added for tun2socks
  ip rule del table 100 2>/dev/null
  ip route flush table 100 2>/dev/null
}

start_tun2socks() {
  log "starting tun2socks on $TUN2SOCKS_TUN via SOCKS5 $SOCKS5_ADDR"

  # Create TUN interface for tun2socks
  cleanup_tun

  # Start tun2socks
  nohup "$TUN2SOCKS_BIN" \
    --device "$TUN2SOCKS_TUN" \
    --proxy "socks5://$SOCKS5_ADDR" \
    --loglevel warning \
    >> "$LOG_FILE" 2>&1 &
  echo "$!" > "$TUN2SOCKS_PID"

  # Wait for tun2socks to create the interface
  for i in 1 2 3 4 5 6 7 8 9 10; do
    ip link show "$TUN2SOCKS_TUN" >/dev/null 2>&1 && break
    sleep 0.5
  done

  if ! ip link show "$TUN2SOCKS_TUN" >/dev/null 2>&1; then
    log "ERROR: tun2socks failed to create $TUN2SOCKS_TUN"
    kill_pid_file "$TUN2SOCKS_PID" "tun2socks"
    return 1
  fi

  # Configure the interface
  ip addr add "$TUN2SOCKS_ADDR" dev "$TUN2SOCKS_TUN"
  ip link set "$TUN2SOCKS_TUN" up

  log "tun2socks started successfully on $TUN2SOCKS_TUN"
  return 0
}

start_openvpn() {
  LOCATION="$1"
  OVPN_FILE="$CONFIGS_DIR/${LOCATION}.ovpn"

  if [ ! -f "$OVPN_FILE" ]; then
    log "ERROR: config not found: $OVPN_FILE"
    echo "error: config not found: ${LOCATION}.ovpn"
    return 1
  fi

  if [ ! -f "$AUTH_FILE" ]; then
    log "ERROR: auth.txt not found at $AUTH_FILE"
    echo "error: auth.txt not found"
    return 1
  fi

  log "starting openvpn with config $LOCATION"

  # Extract remote server from .ovpn
  REMOTE_HOST="$(grep -E '^remote ' "$OVPN_FILE" | head -1 | awk '{print $2}')"
  REMOTE_PORT="$(grep -E '^remote ' "$OVPN_FILE" | head -1 | awk '{print $3}')"

  if [ -z "$REMOTE_HOST" ]; then
    log "ERROR: cannot parse remote host from $OVPN_FILE"
    return 1
  fi

  # Resolve hostname to IP NOW (before tun2socks messes with routing/DNS)
  # Use the RESOLVED_IP that was set in do_start() before tun2socks started
  REMOTE_IP="${RESOLVED_REMOTE_IP:-}"
  if [ -z "$REMOTE_IP" ]; then
    # Fallback: try resolving now (may fail if tun2socks is already up)
    REMOTE_IP="$(getent hosts "$REMOTE_HOST" 2>/dev/null | awk '{print $1}' | head -1)"
  fi
  if [ -z "$REMOTE_IP" ]; then
    REMOTE_IP="$(nslookup "$REMOTE_HOST" 2>/dev/null | grep -A2 'Name:' | grep 'Address' | awk '{print $2}' | head -1)"
  fi
  if [ -z "$REMOTE_IP" ]; then
    log "ERROR: cannot resolve $REMOTE_HOST to IP"
    echo "error: cannot resolve VPN server hostname: $REMOTE_HOST"
    return 1
  fi

  log "resolved $REMOTE_HOST -> $REMOTE_IP"

  # Set library path (fallback for non-static builds)
  export LD_LIBRARY_PATH="/system/lib64:/system/vendor/lib64:/system/apex/com.android.runtime/lib64:${LD_LIBRARY_PATH:-}"

  # Ensure openvpn.log exists so --log-append doesn't fail
  touch "$RUN_DIR/openvpn.log"

  # Android has no /tmp — create it and use our run dir as tmp-dir
  mkdir -p /tmp

  # Write a patched config that forces the resolved IP (bypasses DNS inside OpenVPN)
  # Also remove any tmp-dir directive from the .ovpn since we pass our own
  PATCHED_OVPN="$RUN_DIR/current.ovpn"
  sed -e "s|^remote ${REMOTE_HOST} |remote ${REMOTE_IP} |g" \
      -e '/^tmp-dir/d' \
      "$OVPN_FILE" > "$PATCHED_OVPN"

  # Use OpenVPN's native SOCKS5 proxy support to route through SSH tunnel
  # This is more reliable than routing through tun2socks TUN device
  nohup "$OPENVPN_BIN" \
    --config "$PATCHED_OVPN" \
    --auth-user-pass "$AUTH_FILE" \
    --socks-proxy 127.0.0.1 1080 \
    --dev tun0 \
    --dev-type tun \
    --route-noexec \
    --script-security 0 \
    --tmp-dir "$RUN_DIR" \
    --log-append "$RUN_DIR/openvpn.log" \
    --writepid "$OPENVPN_PID" \
    --connect-retry 5 \
    --connect-timeout 30 \
    --resolv-retry 3 \
    --verb 3 \
    > "$RUN_DIR/openvpn_stdout.log" 2>&1 &
  OVPN_PID="$!"

  # Wait for OpenVPN to write its PID and create tun0
  sleep 2
  if [ ! -f "$OPENVPN_PID" ]; then
    echo "$OVPN_PID" > "$OPENVPN_PID"
  fi

  # Wait for tun0 to come up (max 30s)
  for i in $(seq 1 30); do
    ip link show tun0 >/dev/null 2>&1 && break
    if ! pid_alive "$OVPN_PID"; then
      log "ERROR: openvpn exited prematurely (exit within ${i}s)"
      # Capture whatever output openvpn produced for debugging
      if [ -s "$RUN_DIR/openvpn.log" ]; then
        log "--- openvpn.log tail ---"
        tail -20 "$RUN_DIR/openvpn.log" >> "$LOG_FILE"
      fi
      if [ -s "$RUN_DIR/openvpn_stdout.log" ]; then
        log "--- openvpn stdout/stderr ---"
        tail -20 "$RUN_DIR/openvpn_stdout.log" >> "$LOG_FILE"
      fi
      echo "error: openvpn exited. check $RUN_DIR/openvpn.log and $RUN_DIR/openvpn_stdout.log"
      return 1
    fi
    sleep 1
  done

  if ! ip link show tun0 >/dev/null 2>&1; then
    log "ERROR: tun0 did not come up within 30s"
    kill_pid_file "$OPENVPN_PID" "openvpn"
    return 1
  fi

  # Set up routing using UID-based fwmark
  # Only route app traffic (UID >= 10000) through tun0
  # Root/system (UID 0 = SSHCustom, OpenVPN) stays on original route
  sleep 1

  VPN_LOCAL_IP="$(ip -4 addr show tun0 2>/dev/null | grep -oE 'inet [0-9.]+' | awk '{print $2}')"

  # Step 1: Create routing table 200 with default via tun0
  ip route flush table 200 2>/dev/null
  ip route add default dev tun0 table 200 2>/dev/null

  # Step 2: Policy rule — packets with fwmark 0x1 use table 200
  ip rule del fwmark 0x1 table 200 2>/dev/null
  ip rule add fwmark 0x1 table 200 priority 100 2>/dev/null

  # Step 3: iptables mangle — mark OUTPUT packets from apps (UID >= 10000)
  # This does NOT touch root (UID 0) traffic, so SSHCustom stays unaffected
  iptables -t mangle -D OUTPUT -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t mangle -A OUTPUT -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null

  # Step 4: Bypass SSHCustom's transparent proxy for app traffic
  # SSHCustom uses nat OUTPUT REDIRECT to port 10810 to intercept all TCP.
  # We insert a rule at the TOP of nat OUTPUT that says:
  # "If packet is from an app (UID 10000-99999), RETURN (don't redirect)"
  # This lets the packet fall through to our fwmark routing instead.
  iptables -t nat -D OUTPUT -m owner --uid-owner 10000-99999 -j RETURN 2>/dev/null
  iptables -t nat -I OUTPUT 1 -m owner --uid-owner 10000-99999 -j RETURN 2>/dev/null

  # Step 5: NAT masquerade for traffic going out tun0
  iptables -t nat -D POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null
  iptables -t nat -A POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null

  # Step 6: Also use Windscribe's DNS (pushed as 10.255.255.1)
  # Mark DNS packets from apps so they also go through tun0
  iptables -t mangle -D OUTPUT -p udp --dport 53 -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t mangle -A OUTPUT -p udp --dport 53 -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null

  log "routing configured: UID-based fwmark, apps->tun0, root->original route, VPN_IP=$VPN_LOCAL_IP"

  log "openvpn connected via $LOCATION (remote=$REMOTE_HOST:$REMOTE_PORT)"
  return 0
}

do_start() {
  LOCATION="$1"
  if [ -z "$LOCATION" ]; then
    echo "error: usage: vpnchain start <location>"
    return 1
  fi

  # Check prerequisites
  if [ ! -x "$TUN2SOCKS_BIN" ]; then
    echo "error: tun2socks binary not found at $TUN2SOCKS_BIN"
    return 1
  fi
  if [ ! -x "$OPENVPN_BIN" ]; then
    echo "error: openvpn binary not found at $OPENVPN_BIN"
    return 1
  fi

  # Check if already running
  if read_pid "$TUN2SOCKS_PID" >/dev/null 2>&1; then
    echo "error: vpnchain already running. stop first or use switch."
    return 1
  fi

  log "=== VPN Chain START: location=$LOCATION ==="

  # Resolve the VPN server hostname NOW, before tun2socks changes routing/DNS
  OVPN_FILE="$CONFIGS_DIR/${LOCATION}.ovpn"
  REMOTE_HOST="$(grep -E '^remote ' "$OVPN_FILE" | head -1 | awk '{print $2}')"
  if [ -n "$REMOTE_HOST" ]; then
    RESOLVED_REMOTE_IP="$(getent hosts "$REMOTE_HOST" 2>/dev/null | awk '{print $1}' | head -1)"
    if [ -z "$RESOLVED_REMOTE_IP" ]; then
      RESOLVED_REMOTE_IP="$(nslookup "$REMOTE_HOST" 2>/dev/null | grep -A2 'Name:' | grep 'Address' | awk '{print $2}' | head -1)"
    fi
    if [ -z "$RESOLVED_REMOTE_IP" ]; then
      # Try ping -based resolution
      RESOLVED_REMOTE_IP="$(ping -c1 -W2 "$REMOTE_HOST" 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
    fi
    if [ -z "$RESOLVED_REMOTE_IP" ]; then
      echo "error: cannot resolve VPN server: $REMOTE_HOST (check DNS/internet)"
      write_state "false" "" ""
      return 1
    fi
    log "pre-resolved $REMOTE_HOST -> $RESOLVED_REMOTE_IP"
    export RESOLVED_REMOTE_IP
  fi

  # Step 1: Start tun2socks
  if ! start_tun2socks; then
    echo "error: tun2socks failed to start"
    write_state "false" "" ""
    return 1
  fi

  # Step 2: Start OpenVPN through tun2socks
  if ! start_openvpn "$LOCATION"; then
    # Cleanup tun2socks on failure
    kill_pid_file "$TUN2SOCKS_PID" "tun2socks"
    cleanup_tun
    write_state "false" "" ""
    return 1
  fi

  VPN_IP="$(get_vpn_ip)"
  write_state "true" "$LOCATION" "$VPN_IP"
  log "=== VPN Chain ACTIVE: location=$LOCATION ip=$VPN_IP ==="
  echo "ok: vpnchain started ($LOCATION) ip=$VPN_IP"
  return 0
}

do_stop() {
  log "=== VPN Chain STOP ==="

  # Remove iptables rules (mangle marks + NAT + proxy bypass)
  iptables -t mangle -D OUTPUT -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t mangle -D OUTPUT -p udp --dport 53 -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t nat -D OUTPUT -m owner --uid-owner 10000-99999 -j RETURN 2>/dev/null
  iptables -t nat -D POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null

  # Remove policy routing
  ip rule del fwmark 0x1 table 200 2>/dev/null
  ip route flush table 200 2>/dev/null

  cleanup_routes

  # Kill OpenVPN
  kill_pid_file "$OPENVPN_PID" "openvpn"
  # Also try killall in case PID file was stale
  killall openvpn 2>/dev/null

  # Kill tun2socks
  kill_pid_file "$TUN2SOCKS_PID" "tun2socks"
  killall tun2socks 2>/dev/null

  # Cleanup interfaces
  cleanup_tun
  ip link set tun0 down 2>/dev/null

  # Clear state
  write_state "false" "" ""
  rm -f "$RUN_DIR/openvpn.log"

  log "=== VPN Chain STOPPED ==="
  echo "ok: vpnchain stopped"
  return 0
}

do_switch() {
  LOCATION="$1"
  if [ -z "$LOCATION" ]; then
    echo "error: usage: vpnchain switch <location>"
    return 1
  fi

  OVPN_FILE="$CONFIGS_DIR/${LOCATION}.ovpn"
  if [ ! -f "$OVPN_FILE" ]; then
    echo "error: config not found: ${LOCATION}.ovpn"
    return 1
  fi

  log "=== VPN Chain SWITCH to $LOCATION ==="

  # Resolve VPN server hostname before switching (DNS may break during switch)
  OVPN_FILE="$CONFIGS_DIR/${LOCATION}.ovpn"
  REMOTE_HOST="$(grep -E '^remote ' "$OVPN_FILE" | head -1 | awk '{print $2}')"
  if [ -n "$REMOTE_HOST" ]; then
    RESOLVED_REMOTE_IP="$(getent hosts "$REMOTE_HOST" 2>/dev/null | awk '{print $1}' | head -1)"
    if [ -z "$RESOLVED_REMOTE_IP" ]; then
      RESOLVED_REMOTE_IP="$(nslookup "$REMOTE_HOST" 2>/dev/null | grep -A2 'Name:' | grep 'Address' | awk '{print $2}' | head -1)"
    fi
    if [ -z "$RESOLVED_REMOTE_IP" ]; then
      RESOLVED_REMOTE_IP="$(ping -c1 -W2 "$REMOTE_HOST" 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
    fi
    if [ -n "$RESOLVED_REMOTE_IP" ]; then
      log "pre-resolved $REMOTE_HOST -> $RESOLVED_REMOTE_IP"
      export RESOLVED_REMOTE_IP
    fi
  fi

  # Only kill OpenVPN, keep tun2socks running
  # Clean up routing rules (will be re-added by start_openvpn)
  iptables -t mangle -D OUTPUT -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t mangle -D OUTPUT -p udp --dport 53 -m owner --uid-owner 10000-99999 -j MARK --set-mark 0x1 2>/dev/null
  iptables -t nat -D OUTPUT -m owner --uid-owner 10000-99999 -j RETURN 2>/dev/null
  iptables -t nat -D POSTROUTING -o tun0 -j MASQUERADE 2>/dev/null
  ip rule del fwmark 0x1 table 200 2>/dev/null
  ip route flush table 200 2>/dev/null
  kill_pid_file "$OPENVPN_PID" "openvpn"
  killall openvpn 2>/dev/null
  ip link set tun0 down 2>/dev/null
  sleep 1

  # Restart OpenVPN with new config
  if ! start_openvpn "$LOCATION"; then
    write_state "false" "" ""
    echo "error: switch failed, openvpn did not start"
    return 1
  fi

  VPN_IP="$(get_vpn_ip)"
  write_state "true" "$LOCATION" "$VPN_IP"
  log "=== VPN Chain SWITCHED to $LOCATION ip=$VPN_IP ==="
  echo "ok: vpnchain switched to $LOCATION ip=$VPN_IP"
  return 0
}

do_status() {
  if [ -f "$STATE_FILE" ]; then
    # Verify processes are actually alive
    T2S_ALIVE="false"
    OVPN_ALIVE="false"
    read_pid "$TUN2SOCKS_PID" >/dev/null 2>&1 && T2S_ALIVE="true"
    read_pid "$OPENVPN_PID" >/dev/null 2>&1 && OVPN_ALIVE="true"

    if [ "$T2S_ALIVE" = "true" ] && [ "$OVPN_ALIVE" = "true" ]; then
      # Refresh IP
      VPN_IP="$(get_vpn_ip)"
      LOCATION="$(cat "$STATE_FILE" 2>/dev/null | grep -oE '"location": *"[^"]*"' | sed 's/.*: *"//;s/"//')"
      write_state "true" "$LOCATION" "$VPN_IP"
      cat "$STATE_FILE"
    else
      write_state "false" "" ""
      cat "$STATE_FILE"
    fi
  else
    write_state "false" "" ""
    cat "$STATE_FILE"
  fi
}

do_locations() {
  # List available .ovpn configs (filename without extension)
  if [ -d "$CONFIGS_DIR" ]; then
    ls "$CONFIGS_DIR" 2>/dev/null | grep '\.ovpn$' | sed 's/\.ovpn$//' | sort
  fi
}

# --- Main ---
case "${1:-}" in
  start)   do_start "${2:-}" ;;
  stop)    do_stop ;;
  switch)  do_switch "${2:-}" ;;
  status)  do_status ;;
  locations) do_locations ;;
  *)
    echo "Usage: vpnchain {start <location>|stop|switch <location>|status|locations}"
    exit 2
    ;;
esac
