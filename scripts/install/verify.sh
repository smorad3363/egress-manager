#!/bin/sh
set -eu

config_path="${EGRESS_CONFIG_PATH:-/etc/egress-manager/config.json}"
key_path="${EGRESS_KEY_PATH:-/etc/egress-manager/ipc.key}"

[ "$(id -u)" -eq 0 ] || { echo "verify.sh: run as root" >&2; exit 1; }
systemctl is-active --quiet egressd.service
systemctl is-active --quiet egress-web.service
/usr/local/lib/egress-manager/bin/egressctl status --config "${config_path}" --ipc-key "${key_path}"
/usr/local/lib/egress-manager/bin/sing-box version >/dev/null
/usr/local/lib/egress-manager/bin/xray version >/dev/null
if [ -x /usr/local/lib/egress-manager/bin/lego ]; then /usr/local/lib/egress-manager/bin/lego --version >/dev/null; fi
[ -x /usr/local/lib/egress-manager/manager.sh ] || { echo "verify.sh: management utility is missing" >&2; exit 1; }
[ -L /usr/local/bin/egress-manager ] || { echo "verify.sh: egress-manager command link is missing" >&2; exit 1; }
/usr/local/bin/egress-manager --help >/dev/null
[ -f /usr/local/lib/egress-manager/web/index.html ] || { echo "verify.sh: browser UI is missing" >&2; exit 1; }

panel_port="$(sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' "${config_path}")"
panel_address="$(sed -n 's/^[[:space:]]*"listen_address":[[:space:]]*"\([^"]*\)",*$/\1/p' "${config_path}")"
tls_certificate="$(sed -n 's/^[[:space:]]*"tls_certificate_path":[[:space:]]*"\([^"]*\)",*$/\1/p' "${config_path}")"
[ -n "${panel_port}" ] || { echo "verify.sh: cannot read listen_port" >&2; exit 1; }
[ -n "${panel_address}" ] || { echo "verify.sh: cannot read listen_address" >&2; exit 1; }

scheme="http"
curl_tls=""
if [ -n "${tls_certificate}" ]; then
  [ -r "${tls_certificate}" ] || { echo "verify.sh: TLS certificate is missing" >&2; exit 1; }
  scheme="https"
  curl_tls="-k"
fi
# shellcheck disable=SC2086
curl ${curl_tls} --fail --silent --show-error --max-time 10 "${scheme}://127.0.0.1:${panel_port}/api/v1/health" >/dev/null
# shellcheck disable=SC2086
panel_html="$(curl ${curl_tls} --fail --silent --show-error --max-time 10 "${scheme}://127.0.0.1:${panel_port}/")"
printf '%s' "${panel_html}" | grep -q 'id="root"' || { echo "verify.sh: panel index is not being served" >&2; exit 1; }
printf 'Egress Manager is healthy on %s://%s:%s\n' "${scheme}" "${panel_address}" "${panel_port}"
if [ "${panel_address}" = "0.0.0.0" ]; then
  if [ -r /etc/egress-manager/tls/ip-address ]; then
    server_ip="$(sed -n '1p' /etc/egress-manager/tls/ip-address)"
  else
    server_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
  fi
  [ -n "${server_ip}" ] || server_ip="SERVER_IP"
  printf 'Browser panel: %s://%s:%s/login\n' "${scheme}" "${server_ip}" "${panel_port}"
else
  printf 'Browser panel: %s://%s:%s/login\n' "${scheme}" "${panel_address}" "${panel_port}"
fi
