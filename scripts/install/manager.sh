#!/bin/sh
set -eu

repository="smorad3363/egress-manager"
config="${EGRESS_CONFIG_PATH:-/etc/egress-manager/config.json}"
verify="/usr/local/lib/egress-manager/verify.sh"
web_bin="/usr/local/lib/egress-manager/bin/egress-web"
version_file="/usr/local/lib/egress-manager/VERSION"
runtime_versions="/usr/local/lib/egress-manager/RUNTIME_VERSIONS"

need_root() {
  [ "$(id -u)" -eq 0 ] || { echo "Run this command as root (sudo egress-manager $*)" >&2; exit 1; }
}

need_config() {
  [ -f "${config}" ] || { echo "Configuration not found: ${config}" >&2; exit 1; }
}

json_raw() {
  need_config
  python3 - "${config}" "$1" <<'PY'
import json, sys
path, key = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    data = json.load(handle)
if key not in data:
    raise SystemExit(2)
value = data[key]
if isinstance(value, bool):
    print("true" if value else "false")
elif value is None:
    print("null")
elif isinstance(value, (list, dict)):
    print(json.dumps(value, separators=(",", ":")))
else:
    print(value)
PY
}

panel_port() { json_raw listen_port; }
panel_address() { json_raw listen_address; }
tls_enabled() {
  cert="$(json_raw tls_certificate_path 2>/dev/null || true)"
  key="$(json_raw tls_private_key_path 2>/dev/null || true)"
  [ -n "${cert}" ] && [ "${cert}" != "null" ] && [ -n "${key}" ] && [ "${key}" != "null" ]
}

public_ip() {
  if [ -r /etc/egress-manager/tls/ip-address ]; then
    sed -n '1p' /etc/egress-manager/tls/ip-address
    return
  fi
  ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}'
}

show_url() {
  port="$(panel_port)"
  addr="$(panel_address)"
  scheme="http"
  tls_enabled && scheme="https"
  if [ "${addr}" = "0.0.0.0" ] || [ "${addr}" = "::" ]; then
    ipaddr="$(public_ip || true)"
    [ -n "${ipaddr}" ] || ipaddr="SERVER_IP"
    echo "Panel: ${scheme}://${ipaddr}:${port}/login"
  else
    echo "Panel: ${scheme}://${addr}:${port}/login"
  fi
}

current_version() {
  if [ -r "${version_file}" ]; then
    sed -n '1p' "${version_file}"
  else
    "${web_bin}" --version 2>/dev/null | awk '{print $2; exit}'
  fi
}

show_config() {
  need_config
  python3 - "${config}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    data = json.load(handle)
print(json.dumps(data, indent=2, sort_keys=False))
PY
}

show_config_menu() {
  need_config
  python3 - "${config}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    data = json.load(handle)
for index, (key, value) in enumerate(data.items(), 1):
    rendered = json.dumps(value, separators=(",", ":"))
    print(f"{index:2}) {key} = {rendered}")
PY
}

config_key_by_index() {
  python3 - "${config}" "$1" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    keys = list(json.load(handle).keys())
try:
    index = int(sys.argv[2]) - 1
except ValueError:
    raise SystemExit(2)
if index < 0 or index >= len(keys):
    raise SystemExit(2)
print(keys[index])
PY
}

apply_config_value() {
  need_root "$@"
  need_config
  key="$1"
  shift
  new_value="$*"
  backup="$(mktemp)"
  cp -p "${config}" "${backup}"
  cleanup_config_backup() { rm -f "${backup}"; }
  trap cleanup_config_backup EXIT HUP INT TERM

  if ! python3 - "${config}" "${key}" "${new_value}" <<'PY'
import ipaddress, json, os, re, sys, tempfile
path, key, raw = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    data = json.load(handle)
if key not in data:
    raise SystemExit(f"Unknown config key: {key}")
old = data[key]
try:
    if isinstance(old, bool):
        lowered = raw.strip().lower()
        if lowered in {"true", "1", "yes", "on"}: value = True
        elif lowered in {"false", "0", "no", "off"}: value = False
        else: raise ValueError("expected true/false")
    elif isinstance(old, int) and not isinstance(old, bool):
        value = int(raw)
    elif isinstance(old, float):
        value = float(raw)
    elif isinstance(old, (list, dict)) or old is None:
        value = json.loads(raw)
    else:
        value = raw
except (ValueError, json.JSONDecodeError) as exc:
    raise SystemExit(f"Invalid value for {key}: {exc}")

if key == "listen_port" and not 1024 <= value <= 65535:
    raise SystemExit("listen_port must be between 1024 and 65535")
if key == "listen_address":
    try: ipaddress.ip_address(value)
    except ValueError: raise SystemExit("listen_address must be an IPv4 or IPv6 address")
if key.endswith("_path") or key == "data_directory":
    if not isinstance(value, str) or not os.path.isabs(value):
        raise SystemExit(f"{key} must be an absolute path")
if key == "session_cookie_name":
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", value):
        raise SystemExit("session_cookie_name contains unsupported characters")
if key == "ssh_ports":
    if not isinstance(value, list) or not value or any(not isinstance(p, int) or isinstance(p, bool) or p < 1 or p > 65535 for p in value):
        raise SystemExit("ssh_ports must be a non-empty JSON array of ports 1-65535")
if key == "protected_management_cidrs":
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise SystemExit("protected_management_cidrs must be a JSON array of CIDR strings")
    try:
        for item in value: ipaddress.ip_network(item, strict=False)
    except ValueError as exc:
        raise SystemExit(f"invalid protected_management_cidrs entry: {exc}")

data[key] = value
st = os.stat(path)
directory = os.path.dirname(path)
fd, temporary = tempfile.mkstemp(prefix=".config-manager-", dir=directory, text=True)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(data, handle, indent=2)
        handle.write("\n")
    os.chmod(temporary, 0o640)
    os.chown(temporary, st.st_uid, st.st_gid)
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary): os.unlink(temporary)
PY
  then
    cleanup_config_backup
    trap - EXIT HUP INT TERM
    exit 2
  fi

  systemctl reset-failed egressd.service egress-web.service >/dev/null 2>&1 || true
  if ! systemctl restart egressd.service egress-web.service; then
    echo "New configuration failed to start services; restoring previous configuration." >&2
    install -o root -g egress-manager -m 0640 "${backup}" "${config}"
    systemctl reset-failed egressd.service egress-web.service >/dev/null 2>&1 || true
    systemctl restart egressd.service egress-web.service || true
    cleanup_config_backup
    trap - EXIT HUP INT TERM
    exit 1
  fi

  cleanup_config_backup
  trap - EXIT HUP INT TERM
  echo "Updated ${key}."
}

read_password_twice() {
  tty="/dev/tty"
  [ -r "${tty}" ] && [ -w "${tty}" ] || { echo "A terminal is required to enter a password securely." >&2; return 1; }
  printf "Password (minimum 12 characters): " >"${tty}"
  stty -echo <"${tty}"
  trap 'stty echo </dev/tty 2>/dev/null || true' EXIT HUP INT TERM
  IFS= read -r first <"${tty}"
  stty echo <"${tty}"
  trap - EXIT HUP INT TERM
  printf "\nConfirm password: " >"${tty}"
  stty -echo <"${tty}"
  trap 'stty echo </dev/tty 2>/dev/null || true' EXIT HUP INT TERM
  IFS= read -r second <"${tty}"
  stty echo <"${tty}"
  trap - EXIT HUP INT TERM
  printf "\n" >"${tty}"
  [ "${first}" = "${second}" ] || { echo "Passwords do not match." >&2; unset first second; return 1; }
  [ "$(LC_ALL=C printf '%s' "${first}" | wc -c)" -ge 12 ] || { echo "Password must be at least 12 bytes." >&2; unset first second; return 1; }
  ADMIN_PASSWORD="${first}"
  unset first second
}

create_admin() {
  need_root "$@"
  username="${1:-operator}"
  read_password_twice
  printf '%s\n' "${ADMIN_PASSWORD}" | "${web_bin}" provision-admin --username "${username}" --password-stdin
  unset ADMIN_PASSWORD
}

latest_release() {
  curl -fsSL --retry 3 "https://api.github.com/repos/${repository}/releases?per_page=20" | python3 -c 'import json,sys; releases=json.load(sys.stdin); print(next((r["tag_name"] for r in releases if not r.get("draft") and r.get("tag_name")), ""))'
}

update_manager() {
  need_root "$@"
  command -v curl >/dev/null 2>&1 || { echo "curl is required for online updates." >&2; exit 1; }
  target="${1:-}"
  if [ -z "${target}" ]; then
    echo "Checking GitHub releases..."
    target="$(latest_release)"
  fi
  case "${target}" in v[0-9]* ) ;; *) echo "Invalid release tag: ${target}" >&2; exit 2;; esac
  current="$(current_version || true)"
  if [ "${current}" = "${target}" ]; then
    echo "Already running ${target}."
    return 0
  fi
  echo "Updating ${current:-unknown} -> ${target}"
  temporary="$(mktemp)"
  trap 'rm -f "${temporary}"' EXIT HUP INT TERM
  curl -fsSL --retry 3 "https://raw.githubusercontent.com/${repository}/${target}/scripts/install/install-secure.sh" -o "${temporary}"
  set -- --version "${target}" --skip-admin
  if [ -r /etc/egress-manager/tls/ip-address ]; then
    ipaddr="$(sed -n '1p' /etc/egress-manager/tls/ip-address)"
    [ -n "${ipaddr}" ] && set -- "$@" --ip "${ipaddr}"
  fi
  if [ -r /etc/egress-manager/tls/mode ] && [ "$(sed -n '1p' /etc/egress-manager/tls/mode)" = "self-signed" ]; then
    set -- "$@" --self-signed
  else
    set -- "$@" --public-ca
  fi
  sh "${temporary}" "$@"
  rm -f "${temporary}"
  trap - EXIT HUP INT TERM
  "${verify}"
}

show_tls() {
  if ! tls_enabled; then
    echo "TLS: not configured"
    return
  fi
  mode="unknown"
  [ -r /etc/egress-manager/tls/mode ] && mode="$(sed -n '1p' /etc/egress-manager/tls/mode)"
  echo "TLS mode: ${mode}"
  cert="$(json_raw tls_certificate_path)"
  if [ -r "${cert}" ]; then
    openssl x509 -in "${cert}" -noout -subject -issuer -dates -ext subjectAltName 2>/dev/null || true
  fi
  systemctl --no-pager --full status egress-manager-cert-renew.timer 2>/dev/null || true
}

renew_tls() {
  need_root "$@"
  [ -r /etc/egress-manager/tls/mode ] && [ "$(sed -n '1p' /etc/egress-manager/tls/mode)" = "letsencrypt" ] || { echo "Automatic renewal applies only to Let's Encrypt mode." >&2; exit 1; }
  systemctl start egress-manager-cert-renew.service
  systemctl --no-pager --full status egress-manager-cert-renew.service || true
}

reset_services() { systemctl reset-failed egressd.service egress-web.service >/dev/null 2>&1 || true; }

config_interactive() {
  while :; do
    echo
    echo "Configuration"
    show_config_menu
    echo " 0) Back"
    printf "> "
    read -r choice
    [ "${choice}" = "0" ] && return 0
    key="$(config_key_by_index "${choice}" 2>/dev/null || true)"
    [ -n "${key}" ] || { echo "Unknown choice"; continue; }
    current="$(python3 - "${config}" "${key}" <<'PY'
import json,sys
with open(sys.argv[1], encoding="utf-8") as h: value=json.load(h)[sys.argv[2]]
print(json.dumps(value, separators=(",", ":")))
PY
)"
    echo "Current ${key}: ${current}"
    printf "New value: "
    IFS= read -r new_value
    [ -n "${new_value}" ] || { echo "No change."; continue; }
    "$0" config-set "${key}" "${new_value}"
  done
}

case "${1:-menu}" in
  menu)
    while :; do
      echo
      echo "Egress Manager $(current_version 2>/dev/null || true)"
      echo "1) Status"
      echo "2) Show panel URL"
      echo "3) Restart services"
      echo "4) Start services"
      echo "5) Stop services"
      echo "6) Logs"
      echo "7) Configuration"
      echo "8) Create administrator"
      echo "9) Update Egress Manager"
      echo "10) TLS status"
      echo "11) Renew Let's Encrypt certificate now"
      echo "12) Open panel port in UFW"
      echo "13) Verify installation"
      echo "14) Runtime versions"
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
        7) config_interactive ;;
        8) printf "Username [operator]: "; read -r u; u="${u:-operator}"; "$0" admin "$u" ;;
        9) "$0" update ;;
        10) "$0" tls-status ;;
        11) "$0" tls-renew ;;
        12) "$0" firewall-open ;;
        13) "$0" verify ;;
        14) "$0" versions ;;
        0) exit 0 ;;
        *) echo "Unknown choice" ;;
      esac
    done
    ;;
  status)
    systemctl --no-pager --full status egressd.service egress-web.service || true
    show_url
    ;;
  url) show_url ;;
  start)
    need_root "$@"; reset_services; systemctl start egressd.service egress-web.service; show_url
    ;;
  stop)
    need_root "$@"; systemctl stop egressd.service egress-web.service
    ;;
  restart)
    need_root "$@"; reset_services; systemctl restart egressd.service egress-web.service; show_url
    ;;
  logs) journalctl -u egressd.service -u egress-web.service -n "${2:-100}" --no-pager ;;
  expose)
    need_root "$@"
    tls_enabled || { echo "TLS is not configured. Re-run the secure installer before exposing the panel." >&2; exit 1; }
    apply_config_value listen_address 0.0.0.0
    apply_config_value allow_insecure_http false
    show_url
    ;;
  local)
    need_root "$@"
    apply_config_value listen_address 127.0.0.1
    apply_config_value allow_insecure_http false
    show_url
    ;;
  port)
    [ "$#" -ge 2 ] || { echo "Usage: egress-manager port PORT" >&2; exit 2; }
    apply_config_value listen_port "$2"
    show_url
    ;;
  admin)
    shift
    create_admin "${1:-operator}"
    ;;
  update)
    shift
    update_manager "${1:-}"
    ;;
  config)
    config_interactive
    ;;
  config-show)
    show_config
    ;;
  config-get)
    [ "$#" -ge 2 ] || { echo "Usage: egress-manager config-get KEY" >&2; exit 2; }
    json_raw "$2"
    ;;
  config-set)
    [ "$#" -ge 3 ] || { echo "Usage: egress-manager config-set KEY VALUE" >&2; exit 2; }
    key="$2"; shift 2
    apply_config_value "${key}" "$*"
    ;;
  tls-status) show_tls ;;
  tls-renew) renew_tls "$@" ;;
  verify)
    need_root "$@"; "${verify}"
    ;;
  versions)
    echo "Egress Manager: $(current_version || true)"
    [ -r "${runtime_versions}" ] && cat "${runtime_versions}"
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
    cat <<'EOF_HELP'
Usage:
  egress-manager                         Interactive menu
  egress-manager status|url|start|stop|restart|logs
  egress-manager admin [USER]
  egress-manager update [VERSION]
  egress-manager config
  egress-manager config-show
  egress-manager config-get KEY
  egress-manager config-set KEY VALUE
  egress-manager expose|local|port PORT
  egress-manager tls-status|tls-renew
  egress-manager firewall-open|verify|versions
EOF_HELP
    ;;
  *) echo "Unknown command: $1" >&2; exit 2 ;;
esac
