#!/system/bin/sh
# action.sh — KSU/Magisk action button: toggles the SSH tunnel.
WORK_DIR="/data/adb/sshcustom"
SERVICE="$WORK_DIR/scripts/ssh.service"
RUN_DIR="$WORK_DIR/run"
LOG="$RUN_DIR/action.log"
mkdir -p "$RUN_DIR"
exec 2>&1
{
  echo "========================================"
  echo "     SSHCustom-VPNChain v5.0.0 Action"
  echo "========================================"
  if [ ! -x "$SERVICE" ]; then
    echo "Service script missing: $SERVICE"
    echo "Re-flash the module ZIP to repair."
    exit 1
  fi
  STATUS="$(sh "$SERVICE" status 2>/dev/null | head -1)"
  echo "Status: $STATUS"
  echo
  case "$STATUS" in
    *running*)
      echo "Stopping tunnel..."
      sh "$SERVICE" stop
      echo "Tunnel stopped."
      ;;
    *)
      echo "Starting tunnel..."
      sh "$SERVICE" start
      RC=$?
      if [ "$RC" = "0" ]; then
        echo "Tunnel started."
        echo "Dashboard: http://127.0.0.1:9190/"
      else
        echo "Start failed. Check logs:"
        tail -n 20 "$RUN_DIR/sshcustom.log" 2>/dev/null
        exit "$RC"
      fi
      ;;
  esac
  echo "========================================"
} | tee -a "$LOG"
