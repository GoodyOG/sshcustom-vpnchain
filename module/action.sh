#!/system/bin/sh
# action.sh — KSU/APatch Action button handler
# Produces timestamped output visible in KSU's Action UI.
# Default: toggle (stop if running, start if stopped).

MODDIR="${0%/*}"
WORK_DIR="/data/adb/sshcustom"
SERVICE="${WORK_DIR}/scripts/ssh.service"

printf 'SSHCustom-VPNChain — please wait...\n'

case "${1:-}" in
  start)
    printf '%s [Info]: Starting SSHCustom-VPNChain...\n' "$(date '+%H:%M')"
    sh "${SERVICE}" start
    ;;
  stop)
    printf '%s [Info]: Stopping SSHCustom-VPNChain...\n' "$(date '+%H:%M')"
    sh "${SERVICE}" stop
    ;;
  status)
    sh "${SERVICE}" status
    ;;
  *)
    # Default: toggle
    if sh "${SERVICE}" status >/dev/null 2>&1; then
      printf '%s [Info]: Tunnel running — stopping...\n' "$(date '+%H:%M')"
      sh "${SERVICE}" stop
    else
      printf '%s [Info]: Tunnel stopped — starting...\n' "$(date '+%H:%M')"
      sh "${SERVICE}" start
    fi
    ;;
esac

printf '%s [Info]: Done.\n' "$(date '+%H:%M')"
