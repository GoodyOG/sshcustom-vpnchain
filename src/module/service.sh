#!/system/bin/sh
# service.sh - SSHCustom v2.2.0 boot handler
#
# Boot flow:
#   1. Wait for sys.boot_completed=1 (Android userspace fully up).
#   2. Run boot-reset to clear any stale enabled/paused markers from a previous
#      session. This prevents the daemon from coming up with rules from before
#      the reboot already in iptables.
#   3. ALWAYS start the daemon in idle mode (WebUI accessible at 127.0.0.1:9190).
#   4. If the autostart marker exists ($RUN_DIR/autostart), wait for connectivity
#      (cap 30s) then tell the daemon to start the tunnel via the API.
#
# The daemon is always running so the WebUI is always accessible. The autostart
# flag only controls whether the tunnel connects automatically after boot.

WORK_DIR="/data/adb/sshcustom"
RUN_DIR="$WORK_DIR/run"
LOG="$RUN_DIR/boot.log"
AUTOSTART_MARKER="$RUN_DIR/autostart"
API_CONTROL="http://127.0.0.1:9190/api/v1/control"

mkdir -p "$RUN_DIR"

has_route() {
  ip route get 1.1.1.1 >/dev/null 2>&1 && return 0
  ip route 2>/dev/null | grep -q '^default ' && return 0
  return 1
}

api_start_tunnel() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time 5 -X POST -H 'Content-Type: application/json' -d '{"action":"start"}' "$API_CONTROL" >/dev/null 2>&1 && return 0
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -q -T 5 --post-data='{"action":"start"}' --header='Content-Type: application/json' -O /dev/null "$API_CONTROL" >/dev/null 2>&1 && return 0
  fi
  return 1
}

wait_api_ready() {
  for i in 1 2 3 4 5 6 7 8; do
    if command -v curl >/dev/null 2>&1; then
      curl -fsS --max-time 1 "http://127.0.0.1:9190/api/health" >/dev/null 2>&1 && return 0
    elif command -v wget >/dev/null 2>&1; then
      wget -q -T 1 -O /dev/null "http://127.0.0.1:9190/api/health" >/dev/null 2>&1 && return 0
    fi
    sleep 1
  done
  return 1
}

{
  echo "$(date '+%Y-%m-%d %H:%M:%S') boot service started (v2.2.0)"
  until [ "$(getprop sys.boot_completed 2>/dev/null)" = "1" ]; do sleep 3; done
  echo "$(date '+%Y-%m-%d %H:%M:%S') boot completed; resetting to stopped state"
  [ -x "$WORK_DIR/sshcustom.sh" ] && "$WORK_DIR/sshcustom.sh" boot-reset

  # Always start the daemon in idle mode (WebUI only, no tunnel)
  echo "$(date '+%Y-%m-%d %H:%M:%S') starting daemon in idle mode (WebUI always accessible)"
  if [ -x "$WORK_DIR/sshcustom.sh" ]; then
    "$WORK_DIR/sshcustom.sh" start-idle >> "$LOG" 2>&1
  fi

  if [ -f "$AUTOSTART_MARKER" ]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') autostart enabled; waiting for connectivity (30s cap)"
    i=0
    while [ "$i" -lt 30 ]; do
      if has_route; then
        echo "$(date '+%Y-%m-%d %H:%M:%S') route detected after ${i}s"
        break
      fi
      sleep 1
      i=$((i+1))
    done
    # Wait for daemon API to be ready then start tunnel
    if wait_api_ready; then
      echo "$(date '+%Y-%m-%d %H:%M:%S') daemon API ready; starting tunnel via API"
      api_start_tunnel && echo "$(date '+%Y-%m-%d %H:%M:%S') tunnel start requested" || \
        echo "$(date '+%Y-%m-%d %H:%M:%S') tunnel start API call failed"
    else
      echo "$(date '+%Y-%m-%d %H:%M:%S') daemon API not ready after 8s; tunnel not auto-started"
    fi
  else
    echo "$(date '+%Y-%m-%d %H:%M:%S') autostart disabled; daemon idle (WebUI at 127.0.0.1:9190)"
  fi
} >> "$LOG" 2>&1
exit 0
