#!/system/bin/sh
# ssh.tool — SSHCustom helper utilities
# Modeled after boxproxy/box scripts/box.tool
#
# Usage: ssh.tool <command> [args]
#   blkio          — assign daemon to blkio cgroup (IO priority boost)
#   speed_boost    — apply BBR + TCP buffer tuning
#   speed_restore  — restore original network settings
#   probe_user     — detect daemon UID/GID from PID, print "user:group"
#   boot_id        — print current /proc/sys/kernel/random/boot_id

scripts_dir="${0%/*}"
. /data/adb/sshcustom/settings.ini

# ── Logging helpers ──────────────────────────────────────────────────────────
TOOL_LOG="${box_run}/tool.log"
mkdir -p "$(dirname "${TOOL_LOG}")" >/dev/null 2>&1 || true
# Override log() target so tool output goes to tool.log
log() {
  local level="$1"; shift
  local msg="${current_time} [${level}]: $*"
  printf '%s\n' "${msg}" >> "${TOOL_LOG}"
  if [ -t 1 ]; then printf '%s\n' "${msg}"; fi
}

log Info "ssh.tool called: $*"

# ── probe_user_group ─────────────────────────────────────────────────────────
# Detect UID/GID of the running daemon from /proc/<pid>/status.
# Sets box_user and box_group in calling scope (via stdout parse).
probe_user_group() {
  if [ -f "${box_pid}" ]; then
    PID="$(cat "${box_pid}" 2>/dev/null)"
    if [ -n "${PID}" ] && kill -0 "${PID}" 2>/dev/null; then
      BOX_USER="$(stat -c '%U' /proc/"${PID}" 2>/dev/null || echo root)"
      BOX_GROUP="$(stat -c '%G' /proc/"${PID}" 2>/dev/null || echo root)"
      echo "${BOX_USER}:${BOX_GROUP}"
      log Debug "probe_user_group: pid=${PID} user=${BOX_USER} group=${BOX_GROUP}"
      return 0
    fi
  fi
  # Fallback to root:root
  echo "root:root"
  log Warning "probe_user_group: daemon not running, fallback to root:root"
  return 1
}

# ── cgroup_blkio ─────────────────────────────────────────────────────────────
# Assign daemon PID to blkio cgroup with high IO weight for throughput.
cgroup_blkio() {
  [ "${cgroup_blkio}" = "true" ] || return 0
  [ -f "${box_pid}" ] || { log Warning "blkio: pid file missing"; return 1; }

  local PID
  PID="$(cat "${box_pid}" 2>/dev/null)"
  [ -n "${PID}" ] && kill -0 "${PID}" 2>/dev/null || {
    log Warning "blkio: pid=${PID} not alive"
    return 1
  }

  local weight="${blkio_weight:-900}"

  # Find blkio cgroup mount point
  local blkio_path
  blkio_path="$(mount 2>/dev/null | busybox awk '/blkio/{print $3}' | head -1)"
  if [ -z "${blkio_path}" ] || [ ! -d "${blkio_path}" ]; then
    log Warning "blkio: cgroup mount not found, skipping"
    return 1
  fi

  local target="${blkio_path}/sshcustom"
  if [ ! -d "${target}" ]; then
    mkdir -p "${target}" 2>/dev/null || {
      # Try fallback dirs
      for fallback in foreground top-app apps; do
        [ -d "${blkio_path}/${fallback}" ] && target="${blkio_path}/${fallback}" && break
      done
    }
  fi

  if [ -d "${target}" ]; then
    printf '%s\n' "${weight}" > "${target}/blkio.weight" 2>/dev/null || true
    printf '%s\n' "${PID}" > "${target}/cgroup.procs" 2>/dev/null && {
      log Info "blkio: pid=${PID} → ${target} weight=${weight}"
      return 0
    }
  fi

  log Warning "blkio: failed to assign pid=${PID}"
  return 1
}

# ── apply_speed_boost ────────────────────────────────────────────────────────
# Enable BBR congestion control and/or TCP buffer tuning.
# Saves originals to box_run/speed_orig.env before applying.
apply_speed_boost() {
  local orig_file="${box_run}/speed_orig.env"

  # --- BBR ---
  if [ "${bbr_enabled}" = "true" ]; then
    local avail
    avail="$(sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null)"
    if printf '%s' "${avail}" | grep -q bbr; then
      local orig_cc
      orig_cc="$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo cubic)"
      printf 'orig_congestion_control="%s"\n' "${orig_cc}" >> "${orig_file}"
      sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 && \
        log Info "speed_boost: BBR enabled (was ${orig_cc})" || \
        log Warning "speed_boost: BBR sysctl failed"
    else
      log Warning "speed_boost: BBR not available in kernel (available: ${avail})"
    fi
  fi

  # --- TCP buffer tuning ---
  if [ "${tcp_buffer_tuning}" = "true" ]; then
    local orig_rmem orig_wmem orig_core_r orig_core_w
    orig_core_r="$(sysctl -n net.core.rmem_max 2>/dev/null || echo 212992)"
    orig_core_w="$(sysctl -n net.core.wmem_max 2>/dev/null || echo 212992)"
    orig_rmem="$(sysctl -n net.ipv4.tcp_rmem 2>/dev/null || echo '4096 87380 6291456')"
    orig_wmem="$(sysctl -n net.ipv4.tcp_wmem 2>/dev/null || echo '4096 16384 4194304')"

    {
      printf 'orig_core_rmem_max="%s"\n' "${orig_core_r}"
      printf 'orig_core_wmem_max="%s"\n' "${orig_core_w}"
      printf 'orig_tcp_rmem="%s"\n' "${orig_rmem}"
      printf 'orig_tcp_wmem="%s"\n' "${orig_wmem}"
    } >> "${orig_file}"

    sysctl -w net.core.rmem_max=134217728    >/dev/null 2>&1
    sysctl -w net.core.wmem_max=134217728    >/dev/null 2>&1
    sysctl -w net.ipv4.tcp_rmem="4096 87380 134217728" >/dev/null 2>&1
    sysctl -w net.ipv4.tcp_wmem="4096 65536 134217728" >/dev/null 2>&1
    log Info "speed_boost: TCP buffers set to 128MB max"
  fi
}

# ── restore_speed_settings ───────────────────────────────────────────────────
restore_speed_settings() {
  local orig_file="${box_run}/speed_orig.env"
  [ -f "${orig_file}" ] || return 0

  . "${orig_file}"

  if [ -n "${orig_congestion_control:-}" ]; then
    sysctl -w net.ipv4.tcp_congestion_control="${orig_congestion_control}" >/dev/null 2>&1 && \
      log Info "speed_restore: congestion control restored to ${orig_congestion_control}"
  fi
  if [ -n "${orig_core_rmem_max:-}" ]; then
    sysctl -w net.core.rmem_max="${orig_core_rmem_max}"    >/dev/null 2>&1
    sysctl -w net.core.wmem_max="${orig_core_wmem_max}"    >/dev/null 2>&1
    sysctl -w net.ipv4.tcp_rmem="${orig_tcp_rmem}"         >/dev/null 2>&1
    sysctl -w net.ipv4.tcp_wmem="${orig_tcp_wmem}"         >/dev/null 2>&1
    log Info "speed_restore: TCP buffer settings restored"
  fi

  rm -f "${orig_file}"
}

# ── boot_id ──────────────────────────────────────────────────────────────────
current_boot_id() {
  cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d '-\n'
}

# ── Main dispatch ─────────────────────────────────────────────────────────────
case "${1:-}" in
  blkio)
    cgroup_blkio
    ;;
  speed_boost)
    apply_speed_boost
    ;;
  speed_restore)
    restore_speed_settings
    ;;
  probe_user)
    probe_user_group
    ;;
  boot_id)
    current_boot_id
    ;;
  *)
    log Error "ssh.tool: unknown command '${1:-}'"
    printf 'Usage: %s {blkio|speed_boost|speed_restore|probe_user|boot_id}\n' "$0" >&2
    exit 1
    ;;
esac
