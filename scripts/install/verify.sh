#!/bin/sh
set -eu

config_path="${EGRESS_CONFIG_PATH:-/etc/egress-manager/config.json}"
key_path="${EGRESS_KEY_PATH:-/etc/egress-manager/ipc.key}"

[ "$(id -u)" -eq 0 ] || { echo "verify.sh: run as root" >&2; exit 1; }
systemctl is-active --quiet egressd.service
systemctl is-active --quiet egress-web.service
/usr/local/lib/egress-manager/bin/egressctl status --config "${config_path}" --ipc-key "${key_path}"

panel_port="$(sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' "${config_path}")"
[ -n "${panel_port}" ] || { echo "verify.sh: cannot read listen_port" >&2; exit 1; }
curl --fail --silent --show-error --max-time 10 "http://127.0.0.1:${panel_port}/api/v1/health" >/dev/null
printf 'Egress Manager is healthy on http://127.0.0.1:%s\n' "${panel_port}"
