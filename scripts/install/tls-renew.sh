#!/bin/sh
set -eu

mode_path="/etc/egress-manager/tls/mode"
ip_path="/etc/egress-manager/tls/ip-address"
certificate_path="/etc/egress-manager/tls/server.crt"
private_key_path="/etc/egress-manager/tls/server.key"
acme_directory="/var/lib/egress-manager/acme"
lego="/usr/local/lib/egress-manager/bin/lego"

[ -r "${mode_path}" ] || exit 0
[ "$(sed -n '1p' "${mode_path}")" = "letsencrypt" ] || exit 0
[ -r "${ip_path}" ] || { echo "tls-renew.sh: IP metadata missing" >&2; exit 1; }
server_ip="$(sed -n '1p' "${ip_path}")"
[ -n "${server_ip}" ] || { echo "tls-renew.sh: IP metadata empty" >&2; exit 1; }
[ -x "${lego}" ] || { echo "tls-renew.sh: lego is missing" >&2; exit 1; }

port_free() {
  ! ss -H -ltn | awk -v suffix=":$1" 'substr($4, length($4)-length(suffix)+1) == suffix { found=1 } END { exit !found }'
}
challenge=""
if port_free 80; then challenge="--http"; elif port_free 443; then challenge="--tls"; else
  echo "tls-renew.sh: ACME challenge ports 80 and 443 are occupied" >&2
  exit 1
fi

old_hash=""
[ -f "${certificate_path}" ] && old_hash="$(sha256sum "${certificate_path}" | awk '{print $1}')"

"${lego}" run --accept-tos --path "${acme_directory}" --domains "${server_ip}" \
  --profile shortlived --key-type EC256 --no-random-sleep ${challenge}

cert_source=""
key_source=""
for candidate in "${acme_directory}"/certificates/*.crt; do
  [ -f "${candidate}" ] || continue
  case "${candidate}" in *.issuer.crt) continue ;; esac
  if openssl x509 -in "${candidate}" -noout -ext subjectAltName 2>/dev/null | grep -Fq "IP Address:${server_ip}"; then
    candidate_key="${candidate%.crt}.key"
    [ -s "${candidate_key}" ] || continue
    cert_source="${candidate}"
    key_source="${candidate_key}"
    break
  fi
done
[ -n "${cert_source}" ] || { echo "tls-renew.sh: renewed certificate material not found" >&2; exit 1; }

install -o root -g root -m 0644 "${cert_source}" "${certificate_path}"
install -o root -g egress-manager -m 0640 "${key_source}" "${private_key_path}"
new_hash="$(sha256sum "${certificate_path}" | awk '{print $1}')"
if [ "${new_hash}" != "${old_hash}" ]; then
  systemctl try-restart egress-web.service
fi
