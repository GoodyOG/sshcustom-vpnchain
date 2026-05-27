#!/system/bin/sh

WORK_DIR="/data/adb/sshcustom-vpnchain"
BIN="$WORK_DIR/bin/sshcustomd"
CONFIG="$WORK_DIR/config.json"
PROFILES="$WORK_DIR/profiles.json"
RUN_DIR="$WORK_DIR/run"
PID_FILE="$RUN_DIR/daemon.pid"
WATCHDOG_PID_FILE="$RUN_DIR/watchdog.pid"
ENABLED_FILE="$RUN_DIR/enabled"
PAUSED_FILE="$RUN_DIR/network_paused"
CONTROL_LOG="$RUN_DIR/control.log"
CORE_LOG="$RUN_DIR/core.log"
MODULE_PROP="/data/adb/modules/sshcustom/module.prop"
API_URL="http://127.0.0.1:9190/api/health"

mkdir -p "$RUN_DIR"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$CONTROL_LOG"; }

set_desc() {
  [ -f "$MODULE_PROP" ] || return 0
  case "$1" in
    running) sed -i 's/^description=.*/description=[ 🟢 ] SSHCustom-VPNChain - running/' "$MODULE_PROP" 2>/dev/null ;;
    paused) sed -i 's/^description=.*/description=[ 🟡 ] SSHCustom-VPNChain - paused, waiting for network/' "$MODULE_PROP" 2>/dev/null ;;
    *) sed -i 's/^description=.*/description=[ 🔴 ] SSHCustom-VPNChain - stopped/' "$MODULE_PROP" 2>/dev/null ;;
  esac
}

pid_alive() { PID="$1"; [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; }

daemon_pid() {
  [ -f "$PID_FILE" ] || return 1
  PID="$(cat "$PID_FILE" 2>/dev/null)"
  pid_alive "$PID" || return 1
  echo "$PID"
  return 0
}

is_running() { daemon_pid >/dev/null 2>&1; }

api_alive() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time 1 "$API_URL" >/dev/null 2>&1 && return 0
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -q -T 1 -O /dev/null "$API_URL" >/dev/null 2>&1 && return 0
  fi
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

kill_named_daemon() { killall sshcustomd 2>/dev/null || true; }

has_route() { ip route get 1.1.1.1 >/dev/null 2>&1 && return 0; ip route 2>/dev/null | grep -q '^default ' && return 0; return 1; }

start_watchdog() {
  [ -x "$WORK_DIR/sshcustom_watchdog.sh" ] || return 0
  # Replace stale watchdog; it is low-frequency and only active while module is enabled.
  kill_pid_file "$WATCHDOG_PID_FILE" "watchdog"
  nohup "$WORK_DIR/sshcustom_watchdog.sh" >/dev/null 2>&1 &
  echo "$!" > "$WATCHDOG_PID_FILE"
  log "watchdog start requested pid=$(cat "$WATCHDOG_PID_FILE" 2>/dev/null)"
  return 0
}

start_daemon() {
  if is_running && api_alive; then
    echo "sshcustom runtime already running with pid: $(daemon_pid)"
    return 0
  fi

  rm -f "$PID_FILE"
  kill_named_daemon
  sleep 1

  [ -x "$BIN" ] || { echo "missing binary: $BIN"; return 1; }
  [ -f "$CONFIG" ] || { echo "missing config: $CONFIG"; return 1; }
  [ -f "$PROFILES" ] || { echo "missing profiles: $PROFILES"; return 1; }

  EXTRA_FLAGS=""
  if [ "$1" = "--idle" ]; then
    EXTRA_FLAGS="--idle"
  fi

  nohup "$BIN" run -c "$CONFIG" -p "$PROFILES" -w "$WORK_DIR" $EXTRA_FLAGS >/dev/null 2>&1 &
  PID="$!"
  echo "$PID" > "$PID_FILE"
  log "daemon start requested pid=$PID flags=$EXTRA_FLAGS"

  for i in 1 2 3 4 5; do
    if ! pid_alive "$PID"; then
      echo "sshcustom runtime exited during startup. Check: $CORE_LOG"
      tail -n 20 "$CORE_LOG" 2>/dev/null
      rm -f "$PID_FILE"
      return 1
    fi
    api_alive && { echo "sshcustom runtime started with pid: $PID"; return 0; }
    sleep 1
  done

  if pid_alive "$PID"; then
    echo "sshcustom runtime started with pid: $PID (API still warming up)"
    return 0
  fi
  echo "sshcustom runtime failed. Check: $CORE_LOG"
  tail -n 20 "$CORE_LOG" 2>/dev/null
  rm -f "$PID_FILE"
  return 1
}

stop_runtime() {
  kill_pid_file "$WATCHDOG_PID_FILE" "watchdog"
  kill_pid_file "$PID_FILE" "daemon"
  kill_named_daemon
}

start_module() {
  echo "starting sshcustom module..."
  log "manual/action start v2.0.0"
  mkdir -p "$RUN_DIR"
  : > "$CORE_LOG"
  echo "$(date '+%Y-%m-%d %H:%M:%S') core.log reset for fresh module start" >> "$CORE_LOG"
  touch "$ENABLED_FILE"
  rm -f "$PAUSED_FILE"
  stop_runtime
  "$WORK_DIR/net_clean.sh" >> "$CONTROL_LOG" 2>&1
  if start_daemon; then
    start_watchdog
    set_desc running
    echo "sshcustom module started"
    return 0
  fi
  echo "sshcustom module enabled, but runtime start failed"
  set_desc stopped
  rm -f "$ENABLED_FILE"
  return 1
}

stop_module() {
  echo "stopping sshcustom module..."
  log "manual/action stop"
  rm -f "$ENABLED_FILE" "$PAUSED_FILE"
  stop_runtime
  "$WORK_DIR/net_clean.sh" >> "$CONTROL_LOG" 2>&1
  sleep 1
  "$WORK_DIR/net_clean.sh" >> "$CONTROL_LOG" 2>&1
  set_desc stopped
  echo "sshcustom module stopped and network rules cleaned"
}

start_idle_module() {
  echo "starting sshcustom daemon (idle mode, WebUI only)..."
  log "start-idle: daemon only, no tunnel"
  mkdir -p "$RUN_DIR"
  if is_running && api_alive; then
    echo "sshcustom daemon already running"
    return 0
  fi
  start_daemon --idle
  set_desc stopped
  echo "sshcustom daemon started in idle mode (WebUI at 127.0.0.1:9190)"
}

network_pause() {
  [ -f "$ENABLED_FILE" ] || exit 0
  log "network pause"
  echo "pausing sshcustom runtime until mobile data returns..."
  kill_pid_file "$PID_FILE" "daemon"
  kill_named_daemon
  "$WORK_DIR/net_clean.sh" >> "$CONTROL_LOG" 2>&1
  touch "$PAUSED_FILE"
  set_desc paused
}

network_resume() {
  [ -f "$ENABLED_FILE" ] || exit 0
  log "network resume"
  echo "resuming sshcustom after network returned..."
  : > "$CORE_LOG"
  echo "$(date '+%Y-%m-%d %H:%M:%S') core.log reset for network resume" >> "$CORE_LOG"
  rm -f "$PAUSED_FILE"
  stop_runtime
  "$WORK_DIR/net_clean.sh" >> "$CONTROL_LOG" 2>&1
  if start_daemon; then
    start_watchdog
    set_desc running
    echo "sshcustom runtime resumed"
  else
    touch "$PAUSED_FILE"
    set_desc paused
    echo "sshcustom resume failed; left paused"
    return 1
  fi
}

status_simple() {
  if is_running; then echo "running"; return 0; fi
  if [ -f "$ENABLED_FILE" ]; then echo "enabled"; return 0; fi
  echo "stopped"
}

status_full() {
  echo "SSHCustom status: $(status_simple)"
  [ -f "$PAUSED_FILE" ] && echo "network_paused: yes"
  if is_running; then echo "pid: $(daemon_pid)"; fi
  api_alive && echo "api: online http://127.0.0.1:9190" || echo "api: offline http://127.0.0.1:9190"
  echo "work_dir: $WORK_DIR"
}

boot_reset() {
  log "boot reset"
  rm -f "$ENABLED_FILE" "$PAUSED_FILE"
  stop_runtime
  "$WORK_DIR/net_clean.sh" >> "$CONTROL_LOG" 2>&1
  set_desc stopped
}

case "$1" in
  start) start_module ;;
  start-idle) start_idle_module ;;
  stop) stop_module ;;
  restart) stop_module; sleep 2; start_module ;;
  status) status_full ;;
  status-simple) status_simple ;;
  clean) "$WORK_DIR/net_clean.sh" ;;
  network-pause) network_pause ;;
  network-resume) network_resume ;;
  boot-reset) boot_reset ;;
  *) echo "Usage: $0 {start|start-idle|stop|restart|status|clean|network-pause|network-resume|boot-reset}"; exit 2 ;;
esac
