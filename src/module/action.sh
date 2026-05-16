#!/system/bin/sh
WORK_DIR="/data/adb/sshcustom"
RUN_DIR="$WORK_DIR/run"
CONTROL="$WORK_DIR/sshcustom.sh"
LOG="$RUN_DIR/action.log"
mkdir -p "$RUN_DIR"
exec 2>&1
{
  echo "========================================"
  echo "        SSHCustom-VPNChain Action"
  echo "========================================"
  if [ ! -x "$CONTROL" ]; then
    echo "Control script missing: $CONTROL"
    exit 1
  fi
  STATUS="$($CONTROL status-simple 2>/dev/null)"
  echo "Status: $STATUS"
  echo
  if [ "$STATUS" = "running" ]; then
    echo "Stopping SSHCustom module..."
    "$CONTROL" stop
    echo
    echo "Result: SSHCustom module stopped."
  else
    echo "Starting SSHCustom module..."
    "$CONTROL" start
    RC=$?
    echo
    if [ "$RC" = "0" ]; then
      echo "Result: SSHCustom daemon started."
      echo "Dashboard: http://127.0.0.1:9190/"
    else
      echo "Result: start failed. Last core log lines:"
      tail -n 30 "$RUN_DIR/core.log" 2>/dev/null
      exit "$RC"
    fi
  fi
  echo "Log: $LOG"
  echo "========================================"
} | tee -a "$LOG"
