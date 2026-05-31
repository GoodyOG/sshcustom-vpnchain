#!/system/bin/sh

WORK_DIR="/data/adb/sshcustom"
RUN_DIR="$WORK_DIR/run"
CONTROL="$WORK_DIR/sshcustom.sh"
ENABLED_FILE="$RUN_DIR/enabled"
PAUSED_FILE="$RUN_DIR/network_paused"
LOG="$RUN_DIR/watchdog.log"
LOCK="$RUN_DIR/watchdog.lock"
TOKEN_FILE="$RUN_DIR/watchdog.token"

mkdir -p "$RUN_DIR"
TOKEN="$$-$(date +%s)"
echo "$TOKEN" > "$TOKEN_FILE"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$LOG"; }

am_current() { [ "$(cat "$TOKEN_FILE" 2>/dev/null)" = "$TOKEN" ]; }

has_route() {
  ip route get 1.1.1.1 >/dev/null 2>&1 && return 0
  ip route 2>/dev/null | grep -q '^default ' && return 0
  return 1
}

route_sig() {
  ip route get 1.1.1.1 2>/dev/null | head -n 1 | sed 's/  */ /g'
}

log "watchdog started token=$TOKEN"
log "watchdog active-lite mode: waits for route, restores daemon after long data-off periods"
miss=0
while [ -f "$ENABLED_FILE" ]; do
  am_current || { log "watchdog exiting because newer watchdog is active"; exit 0; }
  if has_route; then
    miss=0
    STATUS="$($CONTROL status-simple 2>/dev/null)"
    if [ "$STATUS" != "running" ]; then
      log "route online but daemon status=$STATUS; requesting resume"
      "$CONTROL" network-resume >> "$LOG" 2>&1 || true
      sleep 12
    else
      rm -f "$PAUSED_FILE" 2>/dev/null
      sleep 10
    fi
  else
    miss=$((miss+1))
    # Do not hammer the daemon when radio is off. After repeated misses, mark paused only.
    [ "$miss" -eq 2 ] && { touch "$PAUSED_FILE"; log "route offline; waiting without reconnect loop"; }
    sleep 10
  fi
done
log "watchdog stopped; module disabled"
exit 0
