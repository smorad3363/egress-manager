#!/bin/sh
set -eu

config="${EGRESS_CONFIG_PATH:-/etc/egress-manager/config.json}"
verify="/usr/local/lib/egress-manager/verify.sh"
web_bin="/usr/local/lib/egress-manager/bin/egress-web"

need_root() {
  [ "$(id -u)" -eq 0 ] || { echo "Run this command as root (sudo egress-manager $*)" >&2; exit 1; }
}

value() {
  key="$1"
  sed -n "s/^[[:space:]]*\"${key}\":[[:space:]]*\"\{0,1\}\([^\",]*\)\"\{0,1\},*$/\1/p" "${config}" | head -n1
}

panel_port() { value listen_port; }
panel_address() { value listen_address; }

public_ip() {
  ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}'
}

show_url() {
  port="$(panel_port)"
  addr="$(panel_address)"
  if [ "${addr}" = "0.0.0.0" ] || [ "${addr}" = "::" ]; then
    ipaddr="$(public_ip || true)"
    [ -n "${ipaddr}" ] || ipaddr="SERVER_IP"
    echo "Panel: http://${ipaddr}:${port}/login"
  else
    echo "Panel: http://${addr}:${port}/login"
  fi
}

set_json_string() {
  key="$1" new_value="$2"
  tmp="$(mktemp)"
  sed "s|^[[:space:]]*\"${key}\":[[:space:]]*\"[^\"]*\"|  \"${key}\": \"${new_value}\"|" "${config}" > "${tmp}"
  install -o root -g egress-manager -m 0640 "${tmp}" "${config}"
  rm -f "${tmp}"
}

set_json_number() {
  key="$1" new_value="$2"
  tmp="$(mktemp)"
  sed "s|^[[:space:]]*\"${key}\":[[:space:]]*[0-9][0-9]*|  \"${key}\": ${new_value}|" "${config}" > "${tmp}"
  install -o root -g egress-manager -m 0640 "${tmp}" "${config}"
  rm -f "${tmp}"
}

set_insecure_http() {
  enabled="$1"
  tmp="$(mktemp)"
  if grep -q '^[[:space:]]*"allow_insecure_http"[[:space:]]*:' "${config}"; then
    sed "s|^[[:space:]]*\"allow_insecure_http\":[[:space:]]*[^,]*|  \"allow_insecure_http\": ${enabled}|" "${config}" > "${tmp}"
  else
    awk -v enabled="${enabled}" '
      { print }
      /^[[:space:]]*"listen_port"[[:space:]]*:/ { print "  \"allow_insecure_http\": " enabled "," }
    ' "${config}" > "${tmp}"
  fi
  install -o root -g egress-manager -m 0640 "${tmp}" "${config}"
  rm -f "${tmp}"
}

case "${1:-menu}" in
  menu)
    while :; do
      echo
      echo "Egress Manager"
      echo "1) Status"
      echo "2) Show panel URL"
      echo "3) Restart"
      echo "4) Start"
      echo "5) Stop"
      echo "6) Logs"
      echo "7) Expose panel on HTTP (0.0.0.0)"
      echo "8) Restrict panel to localhost"
      echo "9) Change panel port"
      echo "10) Provision/reset administrator"
      echo "11) Verify installation"
      echo "12) Open panel port in UFW"
      echo "0) Exit"
      printf "> "
      read -r choice
      case "$choice" in
        1) "$0" status ;;
        2) "$0" url ;;
        3) "$0" restart ;;
        4) "$0" start ;;
        5) "$0" stop ;;
        6) "$0" logs ;;
        7) "$0" expose ;;
        8) "$0" local ;;
        9) printf "Port: "; read -r p; "$0" port "$p" ;;
        10) printf "Username [operator]: "; read -r u; u="${u:-operator}"; "$0" admin "$u" ;;
        11) "$0" verify ;;
        12) "$0" firewall-open ;;
        0) exit 0 ;;
        *) echo "Unknown choice" ;;
      esac
    done
    ;;
  status)
    systemctl --no-pager --full status egressd.service egress-web.service || true
    show_url
    ;;
  url)
    show_url
    ;;
  start|stop|restart)
    need_root "$@"
    systemctl "$1" egressd.service egress-web.service
    [ "$1" = stop ] || show_url
    ;;
  logs)
    journalctl -u egressd.service -u egress-web.service -n 100 --no-pager
    ;;
  expose)
    need_root "$@"
    set_json_string listen_address 0.0.0.0
    set_insecure_http true
    systemctl restart egress-web.service
    echo "Public HTTP mode enabled. Login credentials and session traffic are NOT encrypted."
    show_url
    ;;
  local)
    need_root "$@"
    set_json_string listen_address 127.0.0.1
    set_insecure_http false
    systemctl restart egress-web.service
    show_url
    ;;
  port)
    need_root "$@"
    port="${2:-}"
    case "$port" in ''|*[!0-9]*) echo "Port must be numeric" >&2; exit 2;; esac
    [ "$port" -ge 1024 ] && [ "$port" -le 65535 ] || { echo "Port must be 1024-65535" >&2; exit 2; }
    set_json_number listen_port "$port"
    systemctl restart egress-web.service
    show_url
    ;;
  admin)
    need_root "$@"
    username="${2:-operator}"
    printf "New password for %s: " "$username" >&2
    stty -echo
    trap 'stty echo 2>/dev/null || true' EXIT HUP INT TERM
    IFS= read -r password
    stty echo
    trap - EXIT HUP INT TERM
    echo >&2
    printf '%s\n' "$password" | "$web_bin" provision-admin --username "$username" --password-stdin
    unset password
    ;;
  verify)
    need_root "$@"
    "$verify"
    ;;
  firewall-open)
    need_root "$@"
    command -v ufw >/dev/null 2>&1 || { echo "UFW is not installed. Check your provider/cloud firewall for TCP port $(panel_port)."; exit 0; }
    if ufw status 2>/dev/null | grep -q '^Status: active'; then
      ufw allow "$(panel_port)/tcp"
      echo "Opened TCP $(panel_port) in UFW."
    else
      echo "UFW is inactive. Check your provider/cloud firewall if the panel is still unreachable."
    fi
    show_url
    ;;
  help|-h|--help)
    cat <<'EOF'
Usage: egress-manager [menu|status|url|start|stop|restart|logs|expose|local|port PORT|admin USER|verify|firewall-open]
EOF
    ;;
  *)
    echo "Unknown command: $1" >&2
    exit 2
    ;;
esac
