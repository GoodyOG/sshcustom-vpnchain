#!/system/bin/sh
# service.sh — Boot entrypoint for Magisk/KSU/APatch
# Waits for Android boot, then starts daemon (always in idle mode).
# If autostart marker exists, waits for connectivity and starts the tunnel.

MODDIR="${0%/*}"
WORK_DIR="/data/adb/sshcustom"
RUN_DIR="${WORK_DIR}/run"
SERVICE="${WORK_DIR}/scripts/ssh.service"
LOG="${RUN_DIR}/boot.log"
AUTOSTART_MARKER="${RUN_DIR}/autostart"

mkdir -p "${RUN_DIR}"

{
  printf '%s boot service started\n' "$(date '+%Y-%m-%d %H:%M:%S')"

  # Wait for Android userspace to fully boot
  until [ "$(getprop sys.boot_completed 2>/dev/null)" = "1" ]; do
    sleep 3
  done
  printf '%s sys.boot_completed=1\n' "$(date '+%Y-%m-%d %H:%M:%S')"

  # Always start daemon in idle mode (WebUI always accessible)
  if [ -x "${SERVICE}" ]; then
    printf '%s starting daemon in idle mode\n' "$(date '+%Y-%m-%d %H:%M:%S')"
    sh "${SERVICE}" start-idle
  else
    printf '%s ERROR: service script not found at %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${SERVICE}"
    exit 1
  fi

  # Autostart tunnel if marker exists
  if [ -f "${AUTOSTART_MARKER}" ]; then
    printf '%s autostart enabled — waiting for connectivity (max 30s)\n' "$(date '+%Y-%m-%d %H:%M:%S')"
    i=0
    while [ "${i}" -lt 30 ]; do
      ip route get 1.1.1.1 >/dev/null 2>&1 && break
      sleep 1; i=$((i+1))
    done
    # Wait for API
    j=0
    while [ "${j}" -lt 8 ]; do
      if command -v curl >/dev/null 2>&1; then
        curl -fsS --max-time 1 "http://127.0.0.1:9190/api/v1/health" >/dev/null 2>&1 && break
      fi
      sleep 1; j=$((j+1))
    done
    if command -v curl >/dev/null 2>&1; then
      curl -fsS --max-time 5 -X POST -H 'Content-Type: application/json' \
        -d '{"action":"start"}' "http://127.0.0.1:9190/api/v1/control" >/dev/null 2>&1 && \
        printf '%s tunnel start requested via API\n' "$(date '+%Y-%m-%d %H:%M:%S')" || \
        printf '%s API tunnel start failed\n' "$(date '+%Y-%m-%d %H:%M:%S')"
    else
      sh "${SERVICE}" start
    fi
  else
    printf '%s autostart disabled — daemon idle at 127.0.0.1:9190\n' "$(date '+%Y-%m-%d %H:%M:%S')"
  fi
} >> "${LOG}" 2>&1

exit 0
