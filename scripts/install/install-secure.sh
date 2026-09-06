#!/bin/sh
set -eu

repository="smorad3363/egress-manager"
version="${EGRESS_VERSION:-v0.1.0-alpha.5}"
bundle_root="${EGRESS_BUNDLE_ROOT:-}"
skip_start="${EGRESS_SKIP_START:-0}"
ca_mode="auto"
requested_ip="${EGRESS_SERVER_IP:-}"
admin_mode="${EGRESS_ADMIN_MODE:-auto}"
admin_user="${EGRESS_ADMIN_USER:-operator}"

usage() {
  cat <<'EOF_USAGE'
Usage: install-secure.sh [--version TAG] [--bundle-root DIR] [--ip ADDRESS] [--public-ca|--self-signed] [--skip-start] [--skip-admin|--admin-user USER]

Online mode downloads the complete bundle and attempts a publicly trusted Let's Encrypt
short-lived IP certificate. If public issuance is unavailable, installation continues with
a self-signed certificate whose SAN is the server IP.

On a fresh interactive installation, the installer asks for an administrator username and
password after HTTPS becomes healthy. Use --skip-admin for non-interactive provisioning or
--admin-user USER to force the administrator prompt with a preset username.

When executed directly from a transferred offline bundle, no external CA is contacted by
default and a self-signed IP certificate is created. Use --public-ca to opt into ACME.
EOF_USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; version="$2"; shift 2 ;;
    --bundle-root) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; bundle_root="$2"; shift 2 ;;
    --ip) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; requested_ip="$2"; shift 2 ;;
    --public-ca) ca_mode="public"; shift ;;
    --self-signed) ca_mode="self-signed"; shift ;;
    --skip-start) skip_start=1; shift ;;
    --skip-admin) admin_mode="skip"; shift ;;
    --admin-user) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; admin_user="$2"; admin_mode="prompt"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "install-secure.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

fail() { echo "install-secure.sh: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || fail "run as root (use sudo)"
[ "$(uname -s)" = "Linux" ] || fail "Linux is required"
[ -r /etc/os-release ] || fail "/etc/os-release is unavailable"
. /etc/os-release
[ "${ID:-}" = "ubuntu" ] || fail "supported distributions: Ubuntu 22.04, 24.04, 26.04"
case "${VERSION_ID:-}" in 22.04|24.04|26.04) ubuntu_version="${VERSION_ID}" ;; *) fail "unsupported Ubuntu release: ${VERSION_ID:-unknown}" ;; esac
case "$(uname -m)" in x86_64|amd64) architecture="amd64" ;; aarch64|arm64) architecture="arm64" ;; *) fail "supported architectures: amd64, arm64" ;; esac
case "${admin_mode}" in auto|skip|prompt) ;; *) fail "invalid EGRESS_ADMIN_MODE: ${admin_mode}" ;; esac

script_directory=""
case "$0" in /*|*/*) script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)" ;; esac
if [ -z "${bundle_root}" ] && [ -n "${script_directory}" ] && [ -f "${script_directory}/install-core.sh" ] && [ -d "${script_directory}/package" ]; then
  bundle_root="${script_directory}"
  [ "${ca_mode}" = "auto" ] && ca_mode="self-signed"
fi

if [ -z "${bundle_root}" ]; then
  command -v curl >/dev/null 2>&1 || fail "curl is required for online bootstrap"
  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required for online bootstrap"
  if ! command -v python3 >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends python3-minimal ca-certificates
  fi

  temporary_directory="$(mktemp -d)"
  cleanup_bootstrap() { rm -rf -- "${temporary_directory}"; }
  trap cleanup_bootstrap EXIT HUP INT TERM
  bundle_name="egress-manager-offline-ubuntu${ubuntu_version}-${architecture}"
  release_base="https://github.com/${repository}/releases/download/${version}"
  archive="${temporary_directory}/${bundle_name}.zip"
  checksum="${archive}.sha256"
  echo "Downloading Egress Manager ${version} secure bundle for Ubuntu ${ubuntu_version} ${architecture}..."
  curl --fail --location --silent --show-error --retry 3 --output "${archive}" "${release_base}/${bundle_name}.zip"
  curl --fail --location --silent --show-error --retry 3 --output "${checksum}" "${release_base}/${bundle_name}.zip.sha256"
  expected="$(awk '{print $1}' "${checksum}")"
  actual="$(sha256sum "${archive}" | awk '{print $1}')"
  [ -n "${expected}" ] && [ "${expected}" = "${actual}" ] || fail "release bundle checksum mismatch"
  python3 -m zipfile -e "${archive}" "${temporary_directory}/extracted"
  extracted_root="${temporary_directory}/extracted/${bundle_name}"
  [ -f "${extracted_root}/install.sh" ] || fail "release bundle is incomplete"

  child_ca="--public-ca"
  [ "${ca_mode}" = "self-signed" ] && child_ca="--self-signed"
  set -- --bundle-root "${extracted_root}" "${child_ca}"
  [ -n "${requested_ip}" ] && set -- "$@" --ip "${requested_ip}"
  [ "${skip_start}" = "1" ] && set -- "$@" --skip-start
  [ "${admin_mode}" = "skip" ] && set -- "$@" --skip-admin
  [ "${admin_mode}" = "prompt" ] && set -- "$@" --admin-user "${admin_user}"
  sh "${extracted_root}/install.sh" "$@"
  exit $?
fi

[ "${ca_mode}" = "auto" ] && ca_mode="self-signed"
bundle_root="$(CDPATH= cd -- "${bundle_root}" 2>/dev/null && pwd)" || fail "bundle root does not exist"
core_installer="${bundle_root}/install-core.sh"
package_directory="${bundle_root}/package"
[ -f "${core_installer}" ] || fail "offline bundle has no install-core.sh"
for file in bin/lego tls-renew.sh systemd/egress-manager-cert-renew.service systemd/egress-manager-cert-renew.timer; do
  [ -f "${package_directory}/${file}" ] || fail "secure bundle is incomplete: ${file}"
done

fresh_database=0
[ -e /var/lib/egress-manager/database/egress-manager.db ] || fresh_database=1

# The core installer installs all application/runtime files and dependency closure, but it
# does not start the HTTP service. TLS is configured before the first service start.
sh "${core_installer}" --bundle-root "${bundle_root}" --skip-start --public-http

for command in python3 openssl curl ss ip; do command -v "${command}" >/dev/null 2>&1 || fail "required command missing after bundle installation: ${command}"; done
install -o root -g root -m 0755 "${package_directory}/bin/lego" /usr/local/lib/egress-manager/bin/lego
install -o root -g root -m 0755 "${package_directory}/tls-renew.sh" /usr/local/lib/egress-manager/tls-renew.sh
/usr/local/lib/egress-manager/bin/lego --version >/dev/null

validate_ip() {
  python3 - "$1" <<'PY'
import ipaddress, sys
try:
    ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit(1)
PY
}

server_ip="${requested_ip}"
if [ -z "${server_ip}" ] && [ "${ca_mode}" != "self-signed" ]; then
  server_ip="$(curl -4fsS --max-time 7 https://api.ipify.org 2>/dev/null || true)"
fi
if [ -z "${server_ip}" ]; then
  server_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
fi
[ -n "${server_ip}" ] && validate_ip "${server_ip}" || fail "could not determine a valid server IP; rerun with --ip ADDRESS"

tls_directory="/etc/egress-manager/tls"
certificate_path="${tls_directory}/server.crt"
private_key_path="${tls_directory}/server.key"
mode_path="${tls_directory}/mode"
ip_path="${tls_directory}/ip-address"
acme_directory="/var/lib/egress-manager/acme"
install -d -o root -g egress-manager -m 0750 "${tls_directory}"
install -d -o root -g root -m 0700 "${acme_directory}"
printf '%s\n' "${server_ip}" > "${ip_path}"
chown root:egress-manager "${ip_path}"
chmod 0640 "${ip_path}"

cert_matches_ip() {
  [ -s "${certificate_path}" ] && [ -s "${private_key_path}" ] || return 1
  openssl x509 -in "${certificate_path}" -noout -checkend 86400 >/dev/null 2>&1 || return 1
  openssl x509 -in "${certificate_path}" -noout -ext subjectAltName 2>/dev/null | grep -Fq "IP Address:${server_ip}"
}

previous_mode=""
[ -r "${mode_path}" ] && previous_mode="$(sed -n '1p' "${mode_path}")"
cert_mode="self-signed"
if cert_matches_ip && [ "${previous_mode}" = "letsencrypt" ]; then cert_mode="letsencrypt"; fi

if ! cert_matches_ip; then
  tmp_key="${tls_directory}/server.key.new.$$"
  tmp_cert="${tls_directory}/server.crt.new.$$"
  rm -f "${tmp_key}" "${tmp_cert}"
  openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 3650 \
    -keyout "${tmp_key}" -out "${tmp_cert}" -subj "/CN=${server_ip}" \
    -addext "subjectAltName=IP:${server_ip}" >/dev/null 2>&1
  install -o root -g egress-manager -m 0640 "${tmp_key}" "${private_key_path}"
  install -o root -g root -m 0644 "${tmp_cert}" "${certificate_path}"
  rm -f "${tmp_key}" "${tmp_cert}"
  cert_mode="self-signed"
fi

port_free() {
  ! ss -H -ltn | awk -v suffix=":$1" 'substr($4, length($4)-length(suffix)+1) == suffix { found=1 } END { exit !found }'
}

find_acme_material() {
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
      return 0
    fi
  done
  return 1
}

if [ "${ca_mode}" != "self-signed" ]; then
  challenge=""
  if port_free 80; then challenge="--http"; elif port_free 443; then challenge="--tls"; fi
  if [ -n "${challenge}" ]; then
    echo "Attempting a trusted Let's Encrypt short-lived certificate for ${server_ip}..."
    if /usr/local/lib/egress-manager/bin/lego run \
      --accept-tos --path "${acme_directory}" --domains "${server_ip}" \
      --profile shortlived --key-type EC256 --no-random-sleep ${challenge}; then
      if find_acme_material; then
        install -o root -g root -m 0644 "${cert_source}" "${certificate_path}"
        install -o root -g egress-manager -m 0640 "${key_source}" "${private_key_path}"
        cert_mode="letsencrypt"
      fi
    else
      echo "Public certificate issuance was unavailable; keeping the IP self-signed fallback." >&2
    fi
  else
    echo "Ports 80 and 443 are already occupied; keeping the IP self-signed fallback." >&2
  fi
fi

printf '%s\n' "${cert_mode}" > "${mode_path}"
chown root:egress-manager "${mode_path}"
chmod 0640 "${mode_path}"

python3 - /etc/egress-manager/config.json "${certificate_path}" "${private_key_path}" <<'PY'
import json, os, sys, tempfile
path, cert, key = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    config = json.load(handle)
config["listen_address"] = "0.0.0.0"
config["allow_insecure_http"] = False
config["tls_certificate_path"] = cert
config["tls_private_key_path"] = key
directory = os.path.dirname(path)
fd, tmp = tempfile.mkstemp(prefix=".config-", dir=directory, text=True)
try:
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(config, handle, indent=2)
        handle.write("\n")
    os.chmod(tmp, 0o640)
    os.replace(tmp, path)
finally:
    if os.path.exists(tmp): os.unlink(tmp)
PY
chown root:egress-manager /etc/egress-manager/config.json
chmod 0640 /etc/egress-manager/config.json

install -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-cert-renew.service" /etc/systemd/system/egress-manager-cert-renew.service
install -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-cert-renew.timer" /etc/systemd/system/egress-manager-cert-renew.timer

if [ "${skip_start}" = "1" ]; then
  printf 'Egress Manager secure files installed; service start skipped.\n'
  exit 0
fi

systemctl daemon-reload
systemctl enable egressd.service egress-web.service
if [ "${cert_mode}" = "letsencrypt" ]; then
  systemctl enable --now egress-manager-cert-renew.timer
else
  systemctl disable --now egress-manager-cert-renew.timer >/dev/null 2>&1 || true
fi
systemctl restart egressd.service
systemctl restart egress-web.service

panel_port="$(sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' /etc/egress-manager/config.json)"
[ -n "${panel_port}" ] || fail "cannot read panel port"
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if systemctl is-active --quiet egressd.service && systemctl is-active --quiet egress-web.service && curl -kfsS --max-time 2 "https://127.0.0.1:${panel_port}/api/v1/health" >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[ "${ready}" = "1" ] || { systemctl --no-pager --full status egressd.service egress-web.service >&2 || true; fail "HTTPS services did not become healthy"; }

prompt_admin=0
if [ "${admin_mode}" = "prompt" ]; then
  prompt_admin=1
elif [ "${admin_mode}" = "auto" ] && [ "${fresh_database}" = "1" ] && [ -r /dev/tty ] && [ -w /dev/tty ]; then
  prompt_admin=1
fi

if [ "${prompt_admin}" = "1" ]; then
  [ -r /dev/tty ] && [ -w /dev/tty ] || fail "an interactive terminal is required to provision the administrator"
  if [ "${admin_mode}" = "auto" ]; then
    printf 'Administrator username [%s]: ' "${admin_user}" >/dev/tty
    IFS= read -r entered_user </dev/tty
    [ -n "${entered_user}" ] && admin_user="${entered_user}"
  fi
  printf 'Administrator password (minimum 12 characters): ' >/dev/tty
  stty -echo </dev/tty
  trap 'stty echo </dev/tty 2>/dev/null || true' EXIT HUP INT TERM
  IFS= read -r admin_password </dev/tty
  stty echo </dev/tty
  trap - EXIT HUP INT TERM
  printf '\nConfirm administrator password: ' >/dev/tty
  stty -echo </dev/tty
  trap 'stty echo </dev/tty 2>/dev/null || true' EXIT HUP INT TERM
  IFS= read -r admin_password_confirm </dev/tty
  stty echo </dev/tty
  trap - EXIT HUP INT TERM
  printf '\n' >/dev/tty
  [ "${admin_password}" = "${admin_password_confirm}" ] || fail "administrator passwords do not match"
  [ "$(LC_ALL=C printf '%s' "${admin_password}" | wc -c)" -ge 12 ] || fail "administrator password must be at least 12 bytes"
  printf '%s\n' "${admin_password}" | /usr/local/lib/egress-manager/bin/egress-web provision-admin --username "${admin_user}" --password-stdin
  unset admin_password admin_password_confirm
elif [ "${admin_mode}" = "auto" ] && [ "${fresh_database}" = "1" ]; then
  printf 'Administrator was not provisioned because no interactive terminal is available. Run: sudo egress-manager admin operator\n'
fi

printf 'Installed Egress Manager %s with HTTPS.\n' "$(sed -n '1p' "${bundle_root}/VERSION")"
printf 'Panel: https://%s:%s/login\n' "${server_ip}" "${panel_port}"
if [ "${cert_mode}" = "letsencrypt" ]; then
  printf "TLS: trusted Let's Encrypt IP certificate; automatic renewal enabled.\n"
else
  printf 'TLS: self-signed IP certificate (SAN=%s); browser trust warning is expected until this certificate is trusted.\n' "${server_ip}"
fi
printf 'Management: sudo egress-manager\n'
