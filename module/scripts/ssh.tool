#!/system/bin/sh
# ssh.tool — SSHCustom helper utilities
# Modeled after boxproxy/box scripts/box.tool
#
# Usage: ssh.tool <command>
#   blkio          — assign daemon to blkio cgroup
#   speed_boost    — apply BBR + TCP buffer tuning
#   speed_restore  — restore original network settings
#   probe_user     — print "user:group" of running daemon
#   boot_id        — print current boot_id (stripped of hyphens)

scripts_dir="${0%/*}"
. /data/adb/sshcustom/settings.ini

TOOL_LOG="${box_run}/tool.log"
mkdir -p "$(dirname "${TOOL_LOG}")" >/dev/null 2>&1 || true

# Override log() to write to tool.log
log() {
  local level="$1"; shift
  local _ts
  _ts="$(date '+%H:%M')"
  local msg="${_ts} [${level}]: $*"
  printf '%s\n' "${msg}" >> "${TOOL_LOG}"
  if [ -t 1 ]; then printf '%s\n' "${msg}"; fi
}

log Info "ssh.tool called: $*"

# ── current_boot_id ───────────────────────────────────────────────────────────
# Strips hyphens from boot_id. Uses POSIX-compatible tr (no \n escape).
current_boot_id() {
  # Read boot_id and strip hyphens only (no newline stripping needed for single-line file)
  # Use POSIX-safe character class: tr -d '-'
  # Then strip trailing newline with printf trick
  local raw
  raw="$(cat /proc/sys/kernel/random/boot_id 2>/dev/null)"
  # Strip hyphens using POSIX tr (no \n escape needed here)
  printf '%s' "${raw}" | tr -d '-'
}

# ── probe_user_group ──────────────────────────────────────────────────────────
probe_user_group() {
  if [ -f "${box_pid}" ]; then
    local PID
    PID="$(cat "${box_pid}" 2>/dev/null)"
    if [ -n "${PID}" ] && kill -0 "${PID}" 2>/dev/null; then
      local u g
      u="$(stat -c '%U' /proc/"${PID}" 2>/dev/null || echo root)"
      g="$(stat -c '%G' /proc/"${PID}" 2>/dev/null || echo root)"
      printf '%s:%s\n' "${u}" "${g}"
      log Debug "probe_user_group: pid=${PID} user=${u} group=${g}"
      return 0
    fi
  fi
  printf 'root:root\n'
  log Warning "probe_user_group: daemon not running, using root:root"
  return 1
}

# ── cgroup_blkio ─────────────────────────────────────────────────────────────
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
  blkio_path="$(mount 2>/dev/null | busybox awk '/blkio/{print $3; exit}')"
  if [ -z "${blkio_path}" ] || [ ! -d "${blkio_path}" ]; then
    log Warning "blkio: cgroup mount not found — skipping"
    return 0  # non-fatal: blkio is optional
  fi

  local target="${blkio_path}/sshcustom"
  if [ ! -d "${target}" ]; then
    mkdir -p "${target}" 2>/dev/null
    if [ ! -d "${target}" ]; then
      # Try standard fallback dirs
      local fallback
      for fallback in foreground top-app apps; do
        if [ -d "${blkio_path}/${fallback}" ]; then
          target="${blkio_path}/${fallback}"
          break
        fi
      done
    fi
  fi

  if [ -d "${target}" ]; then
    printf '%s\n' "${weight}" > "${target}/blkio.weight" 2>/dev/null || true
    if printf '%s\n' "${PID}" > "${target}/cgroup.procs" 2>/dev/null; then
      log Info "blkio: pid=${PID} → ${target} weight=${weight}"
      return 0
    fi
  fi

  log Warning "blkio: could not assign pid=${PID} — skipping (non-fatal)"
  return 0  # always succeed — blkio is a nice-to-have
}

# ── apply_speed_boost ─────────────────────────────────────────────────────────
apply_speed_boost() {
  local orig_file="${box_run}/speed_orig.env"
  : > "${orig_file}"

  # BBR congestion control
  if [ "${bbr_enabled}" = "true" ]; then
    local avail
    avail="$(sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null || true)"
    case " ${avail} " in
      *" bbr "*)
        local orig_cc
        orig_cc="$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo cubic)"
        printf 'orig_congestion_control="%s"\n' "${orig_cc}" >> "${orig_file}"
        sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 && \
          log Info "speed_boost: BBR enabled (was ${orig_cc})" || \
          log Warning "speed_boost: BBR sysctl failed"
        ;;
      *)
        log Warning "speed_boost: BBR not in kernel (available: ${avail})"
        ;;
    esac
  fi

  # TCP buffer tuning
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

    sysctl -w net.core.rmem_max=134217728    >/dev/null 2>&1 || true
    sysctl -w net.core.wmem_max=134217728    >/dev/null 2>&1 || true
    sysctl -w net.ipv4.tcp_rmem="4096 87380 134217728" >/dev/null 2>&1 || true
    sysctl -w net.ipv4.tcp_wmem="4096 65536 134217728" >/dev/null 2>&1 || true
    log Info "speed_boost: TCP buffers set to 128MB max"
  fi
}

# ── restore_speed_settings ────────────────────────────────────────────────────
restore_speed_settings() {
  local orig_file="${box_run}/speed_orig.env"
  [ -f "${orig_file}" ] || return 0

  # shellcheck disable=SC1090
  . "${orig_file}"

  if [ -n "${orig_congestion_control:-}" ]; then
    sysctl -w net.ipv4.tcp_congestion_control="${orig_congestion_control}" >/dev/null 2>&1 && \
      log Info "speed_restore: congestion control → ${orig_congestion_control}"
  fi
  if [ -n "${orig_core_rmem_max:-}" ]; then
    sysctl -w net.core.rmem_max="${orig_core_rmem_max}"    >/dev/null 2>&1 || true
    sysctl -w net.core.wmem_max="${orig_core_wmem_max}"    >/dev/null 2>&1 || true
    sysctl -w net.ipv4.tcp_rmem="${orig_tcp_rmem}"         >/dev/null 2>&1 || true
    sysctl -w net.ipv4.tcp_wmem="${orig_tcp_wmem}"         >/dev/null 2>&1 || true
    log Info "speed_restore: TCP buffer settings restored"
  fi

  rm -f "${orig_file}"
}

# ── Main ──────────────────────────────────────────────────────────────────────
case "${1:-}" in
  blkio)         cgroup_blkio ;;
  speed_boost)   apply_speed_boost ;;
  speed_restore) restore_speed_settings ;;
  probe_user)    probe_user_group ;;
  boot_id)       current_boot_id ;;
  *)
    log Error "ssh.tool: unknown command '${1:-}'"
    printf 'Usage: %s {blkio|speed_boost|speed_restore|probe_user|boot_id}\n' "$0" >&2
    exit 1
    ;;
esac
