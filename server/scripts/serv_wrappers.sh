#!/usr/bin/env bash
# Install / remove convenience symlinks: serv_status, serv_health, РІР‚В¦
set -euo pipefail

BIN_DIR="${NYXVEIL_BIN_DIR:-/usr/local/sbin}"
LINK_DIR="${NYXVEIL_LINK_DIR:-/usr/local/bin}"
CTL="${BIN_DIR}/nyxveilctl"

COMMANDS=(status health start stop restart logs version config configure update update_bootstrap uninstall)

usage() {
  cat <<'EOF'
Usage: serv_wrappers.sh install|remove|list

  install  Create /usr/local/bin/serv_* wrappers around nyxveilctl
  remove   Delete those wrappers
  list     Print wrapper names
EOF
}

install_wrappers() {
  [[ "$(id -u)" -eq 0 ]] || { echo "root required for install" >&2; exit 1; }
  [[ -x "${CTL}" ]] || { echo "missing ${CTL}" >&2; exit 1; }
  mkdir -p "${LINK_DIR}"
  local c
  for c in "${COMMANDS[@]}"; do
    if [[ "${c}" == "update_bootstrap" ]]; then
      cat > "${LINK_DIR}/serv_update_bootstrap" <<EOF
#!/usr/bin/env bash
# Legacy РІвЂ°В¤1.0.4: replace ONLY nyxveilctl from signed release, then full update.
exec ${CTL} bootstrap-cli --version "\${NYXVEIL_BOOTSTRAP_VERSION:-1.1.13}" --then-update "\$@"
EOF
      chmod 0755 "${LINK_DIR}/serv_update_bootstrap"
      continue
    fi
    cat > "${LINK_DIR}/serv_${c}" <<EOF
#!/usr/bin/env bash
exec ${CTL} ${c} "\$@"
EOF
    chmod 0755 "${LINK_DIR}/serv_${c}"
  done
  local out=()
  local c
  for c in "${COMMANDS[@]}"; do
    if [[ "${c}" == "update_bootstrap" ]]; then
      out+=("serv_update_bootstrap")
    else
      out+=("serv_${c}")
    fi
  done
  echo "installed: ${out[*]}"
}

remove_wrappers() {
  [[ "$(id -u)" -eq 0 ]] || { echo "root required for remove" >&2; exit 1; }
  local c
  for c in "${COMMANDS[@]}"; do
    if [[ "${c}" == "update_bootstrap" ]]; then
      rm -f "${LINK_DIR}/serv_update_bootstrap"
    else
      rm -f "${LINK_DIR}/serv_${c}"
    fi
  done
  echo "removed serv_* wrappers"
}

list_wrappers() {
  local c
  for c in "${COMMANDS[@]}"; do
    if [[ "${c}" == "update_bootstrap" ]]; then
      echo "serv_update_bootstrap"
    else
      echo "serv_${c}"
    fi
  done
}

case "${1:-}" in
  install) install_wrappers ;;
  remove) remove_wrappers ;;
  list) list_wrappers ;;
  -h|--help|help) usage ;;
  *) usage; exit 2 ;;
esac
