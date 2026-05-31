#!/system/bin/sh
# service.sh — Boot entrypoint for Magisk/KSU/APatch
# Waits for Android boot (max 5 min), then starts daemon in idle mode.
# If autostart marker exists, waits for connectivity then starts the tunnel.

MODDIR="${0%/*}"
WORK_DIR="/data/adb/sshcustom"
RUN_DIR="${WORK_DIR}/run"
SERVICE="${WORK_DIR}/scripts/ssh.service"
LOG="${RUN_DIR}/boot.log"
AUTOSTART_MARKER="${RUN_DIR}/autostart"

mkdir -p "${RUN_DIR}"

{
  printf '%s boot service started\n' "$(date '+%Y-%m-%d %H:%M:%S')"

  # Wait for Android userspace — cap at 5 minutes (100 × 3s)
  _boot_waited=0
  until [ "$(getprop sys.boot_completed 2>/dev/null)" = "1" ]; do
    sleep 3
    _boot_waited=$((_boot_waited + 1))
    if [ "${_boot_waited}" -ge 100 ]; then
      printf '%s boot_completed never became 1 after 5 min — starting anyway\n' \
        "$(date '+%Y-%m-%d %H:%M:%S')"
      break
    fi
  done
  printf '%s boot ready (waited %ds)\n' \
    "$(date '+%Y-%m-%d %H:%M:%S')" "$((_boot_waited * 3))"

  # Always start daemon in idle mode (WebUI always accessible)
  if [ -x "${SERVICE}" ]; then
    printf '%s starting daemon in idle mode\n' "$(date '+%Y-%m-%d %H:%M:%S')"
    sh "${SERVICE}" start-idle
    # Set module.prop to idle state (daemon will update to running when tunnel connects)
    sed -i 's|^description=.*|description=[ 💤 ] SSHCustom-VPNChain idle|' /data/adb/modules/sshcustom-vpnchain/module.prop 2>/dev/null || true
  else
    printf '%s ERROR: service script not found at %s\n' \
      "$(date '+%Y-%m-%d %H:%M:%S')" "${SERVICE}"
    exit 1
  fi

  # Autostart tunnel if marker exists
  if [ -f "${AUTOSTART_MARKER}" ]; then
    printf '%s autostart enabled — waiting for connectivity (max 60s)\n' \
      "$(date '+%Y-%m-%d %H:%M:%S')"

    # Wait for default route (max 60s)
    _route_waited=0
    while [ "${_route_waited}" -lt 60 ]; do
      ip route get 1.1.1.1 >/dev/null 2>&1 && break
      sleep 2; _route_waited=$((_route_waited + 2))
    done

    # Wait for daemon API (max 10s)
    _api_waited=0
    while [ "${_api_waited}" -lt 10 ]; do
      if command -v curl >/dev/null 2>&1; then
        curl -fsS --max-time 1 "http://127.0.0.1:9190/api/v1/health" >/dev/null 2>&1 && break
      fi
      sleep 1; _api_waited=$((_api_waited + 1))
    done

    # Start tunnel via API if available, otherwise via script
    _started=0
    if command -v curl >/dev/null 2>&1; then
      curl -fsS --max-time 5 \
        -X POST -H 'Content-Type: application/json' \
        -d '{"action":"start"}' \
        "http://127.0.0.1:9190/api/v1/control" >/dev/null 2>&1 && _started=1
    fi
    if [ "${_started}" -eq 0 ]; then
      sh "${SERVICE}" start
    fi
    printf '%s autostart tunnel requested\n' "$(date '+%Y-%m-%d %H:%M:%S')"
  else
    printf '%s autostart disabled — daemon idle at 127.0.0.1:9190\n' \
      "$(date '+%Y-%m-%d %H:%M:%S')"
  fi
} >> "${LOG}" 2>&1

exit 0
