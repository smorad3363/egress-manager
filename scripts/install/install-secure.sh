#!/bin/sh
set -eu

repository="smorad3363/egress-manager"
version="${EGRESS_VERSION:-v0.1.0-alpha.9}"
bundle_root="${EGRESS_BUNDLE_ROOT:-}"
skip_start="${EGRESS_SKIP_START:-0}"
ca_mode="auto"
requested_ip="${EGRESS_SERVER_IP:-}"
admin_mode="${EGRESS_ADMIN_MODE:-auto}"
admin_user="${EGRESS_ADMIN_USER:-operator}"
log_file="${EGRESS_INSTALL_LOG:-}"
current_stage="startup"
bootstrap_directory=""
stream_fifo=""
stty_hidden=0

usage() {
  cat <<'EOF_USAGE'
Usage: install-secure.sh [--version TAG] [--bundle-root DIR] [--ip ADDRESS] [--public-ca|--self-signed] [--skip-start] [--skip-admin|--admin-user USER]

Online mode detects Ubuntu/architecture/server IP, downloads the matching release bundle with
visible progress, verifies SHA-256, installs the runtime, configures HTTPS, starts services,
and prints the final panel URL. A complete install log is written under /var/log/egress-manager.

Online mode attempts a publicly trusted Let's Encrypt short-lived IP certificate. If public
issuance is unavailable, installation continues with a self-signed certificate whose SAN is
the server IP.

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

timestamp() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }

log_line() {
  message="$*"
  printf '%s\n' "${message}"
  if [ -n "${log_file}" ]; then
    printf '%s %s\n' "$(timestamp)" "${message}" >> "${log_file}" 2>/dev/null || true
  fi
}

warn() { log_line "WARNING: $*" >&2; }

fail() {
  log_line "ERROR [${current_stage}]: $*" >&2
  exit 1
}

stage() {
  current_stage="$1"
  log_line ""
  log_line "==> ${current_stage}"
}

has_controlling_tty() {
  (exec 3</dev/tty 4>/dev/tty) 2>/dev/null
}

init_logging() {
  if [ -z "${log_file}" ]; then
    install -d -o root -g root -m 0750 /var/log/egress-manager
    log_file="/var/log/egress-manager/install-$(date -u '+%Y%m%dT%H%M%SZ')-$$.log"
  else
    install -d -o root -g root -m 0750 "$(dirname -- "${log_file}")"
  fi
  : >> "${log_file}"
  chmod 0640 "${log_file}" 2>/dev/null || true
  export EGRESS_INSTALL_LOG="${log_file}"
}

cleanup_all() {
  if [ "${stty_hidden}" = "1" ]; then
    stty echo </dev/tty 2>/dev/null || true
    stty_hidden=0
  fi
  if [ -n "${stream_fifo}" ]; then
    rm -f -- "${stream_fifo}" 2>/dev/null || true
  fi
  if [ -n "${bootstrap_directory}" ]; then
    rm -rf -- "${bootstrap_directory}" 2>/dev/null || true
  fi
}

on_exit() {
  rc="$1"
  trap - EXIT HUP INT TERM
  cleanup_all
  if [ "${rc}" -ne 0 ] && [ -n "${log_file}" ] && [ -f "${log_file}" ]; then
    printf '\nInstallation failed during: %s\n' "${current_stage}" >&2
    printf 'Install log: %s\n' "${log_file}" >&2
    printf '%s\n' '----- last 60 log lines -----' >&2
    tail -n 60 "${log_file}" >&2 2>/dev/null || true
    printf '%s\n' '-----------------------------' >&2
  fi
  exit "${rc}"
}

run_live() {
  stream_fifo="/tmp/egress-manager-install.$$.fifo"
  rm -f -- "${stream_fifo}"
  mkfifo "${stream_fifo}" || fail "could not create logging pipe"
  tee -a "${log_file}" < "${stream_fifo}" &
  tee_pid=$!
  set +e
  "$@" > "${stream_fifo}" 2>&1
  rc=$?
  set -e
  wait "${tee_pid}" 2>/dev/null || true
  rm -f -- "${stream_fifo}"
  stream_fifo=""
  return "${rc}"
}

run_logged() {
  "$@" >> "${log_file}" 2>&1
}

[ "$(id -u)" -eq 0 ] || { echo "install-secure.sh: run as root (use sudo)" >&2; exit 1; }
[ "$(uname -s)" = "Linux" ] || { echo "install-secure.sh: Linux is required" >&2; exit 1; }
command -v install >/dev/null 2>&1 || { echo "install-secure.sh: coreutils/install is required" >&2; exit 1; }
init_logging
trap 'on_exit $?' EXIT
trap 'exit 130' HUP INT TERM

stage "Preflight"
[ -r /etc/os-release ] || fail "/etc/os-release is unavailable"
. /etc/os-release
[ "${ID:-}" = "ubuntu" ] || fail "supported distributions: Ubuntu 22.04, 24.04, 26.04"
case "${VERSION_ID:-}" in 22.04|24.04|26.04) ubuntu_version="${VERSION_ID}" ;; *) fail "unsupported Ubuntu release: ${VERSION_ID:-unknown}" ;; esac
case "$(uname -m)" in x86_64|amd64) architecture="amd64" ;; aarch64|arm64) architecture="arm64" ;; *) fail "supported architectures: amd64, arm64" ;; esac
case "${admin_mode}" in auto|skip|prompt) ;; *) fail "invalid EGRESS_ADMIN_MODE: ${admin_mode}" ;; esac
for required in date tail tee mkfifo df awk sed grep wc stty; do
  command -v "${required}" >/dev/null 2>&1 || fail "required base command missing: ${required}"
done
available_tmp_kb="$(df -Pk /tmp 2>/dev/null | awk 'NR==2 {print $4}')"
if [ -n "${available_tmp_kb}" ] && [ "${available_tmp_kb}" -lt 524288 ] 2>/dev/null; then
  fail "less than 512 MiB is available under /tmp; free disk space before installation"
fi
log_line "Version: ${version}"
log_line "Host: Ubuntu ${ubuntu_version} ${architecture}"
log_line "Log: ${log_file}"
if [ -r /usr/local/lib/egress-manager/VERSION ]; then
  log_line "Existing install: $(sed -n '1p' /usr/local/lib/egress-manager/VERSION)"
else
  log_line "Existing install: none detected"
fi

script_directory=""
case "$0" in /*|*/*) script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)" ;; esac
if [ -z "${bundle_root}" ] && [ -n "${script_directory}" ] && [ -f "${script_directory}/install-core.sh" ] && [ -d "${script_directory}/package" ]; then
  bundle_root="${script_directory}"
  [ "${ca_mode}" = "auto" ] && ca_mode="self-signed"
fi

if [ -z "${bundle_root}" ]; then
  command -v curl >/dev/null 2>&1 || fail "curl is required for online bootstrap"
  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required for online bootstrap"

  if ! python3 -c 'import json, zipfile' >/dev/null 2>&1; then
    stage "Repair Python bootstrap"
    export DEBIAN_FRONTEND=noninteractive
    run_live apt-get update || fail "apt-get update failed while repairing Python"
    run_live apt-get install -y --no-install-recommends --no-remove python3 ca-certificates || fail "could not repair the complete Python 3 runtime"
  fi
  python3 -c 'import json, zipfile' >/dev/null 2>&1 || fail "a complete Python 3 standard library is required for the online bootstrap"

  bootstrap_directory="$(mktemp -d)"
  bundle_name="egress-manager-offline-ubuntu${ubuntu_version}-${architecture}"
  release_base="https://github.com/${repository}/releases/download/${version}"
  archive="${bootstrap_directory}/${bundle_name}.zip"
  checksum="${archive}.sha256"

  stage "Download release bundle"
  log_line "Asset: ${bundle_name}.zip"
  run_live curl --fail --location --show-error --retry 3 --retry-delay 2 --retry-all-errors \
    --connect-timeout 10 --max-time 1200 --progress-bar \
    --output "${archive}" "${release_base}/${bundle_name}.zip" || fail "bundle download failed"
  run_logged curl --fail --location --silent --show-error --retry 3 --retry-delay 2 --retry-all-errors \
    --connect-timeout 10 --max-time 120 \
    --output "${checksum}" "${release_base}/${bundle_name}.zip.sha256" || fail "checksum download failed"

  stage "Verify release checksum"
  expected="$(awk 'NR==1 {print $1}' "${checksum}")"
  actual="$(sha256sum "${archive}" | awk '{print $1}')"
  [ -n "${expected}" ] || fail "release checksum file is empty"
  [ "${expected}" = "${actual}" ] || fail "release bundle checksum mismatch (expected ${expected}, got ${actual})"
  log_line "SHA-256: ${actual} OK"

  stage "Extract release bundle"
  run_logged python3 -m zipfile -e "${archive}" "${bootstrap_directory}/extracted" || fail "could not extract release ZIP"
  extracted_root="${bootstrap_directory}/extracted/${bundle_name}"
  [ -f "${extracted_root}/install.sh" ] || fail "release bundle is incomplete"

  child_ca="--public-ca"
  [ "${ca_mode}" = "self-signed" ] && child_ca="--self-signed"
  set -- --bundle-root "${extracted_root}" "${child_ca}"
  [ -n "${requested_ip}" ] && set -- "$@" --ip "${requested_ip}"
  [ "${skip_start}" = "1" ] && set -- "$@" --skip-start
  [ "${admin_mode}" = "skip" ] && set -- "$@" --skip-admin
  [ "${admin_mode}" = "prompt" ] && set -- "$@" --admin-user "${admin_user}"

  stage "Run secure installer"
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

stage "Install runtime and application files"
run_live sh "${core_installer}" --bundle-root "${bundle_root}" --skip-start --public-http || fail "core installation failed"

stage "Validate installed runtime"
for command in python3 openssl curl ss ip; do command -v "${command}" >/dev/null 2>&1 || fail "required command missing after bundle installation: ${command}"; done
python3 -c 'import json, ipaddress, tempfile' >/dev/null 2>&1 || fail "complete Python 3 standard library missing after bundle installation"
install -o root -g root -m 0755 "${package_directory}/bin/lego" /usr/local/lib/egress-manager/bin/lego
install -o root -g root -m 0755 "${package_directory}/tls-renew.sh" /usr/local/lib/egress-manager/tls-renew.sh
/usr/local/lib/egress-manager/bin/lego --version >> "${log_file}" 2>&1 || fail "bundled lego runtime is invalid"

validate_ip() {
  python3 - "$1" <<'PY'
import ipaddress, sys
try:
    ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit(1)
PY
}

stage "Detect server IP"
detected_public_ip="$(curl -4fsS --max-time 7 https://api.ipify.org 2>/dev/null || true)"
server_ip="${requested_ip}"
if [ -n "${requested_ip}" ] && [ -n "${detected_public_ip}" ] && [ "${requested_ip}" != "${detected_public_ip}" ]; then
  warn "requested IP ${requested_ip} differs from detected public IP ${detected_public_ip}; using the explicitly requested IP"
fi
if [ -z "${server_ip}" ]; then
  server_ip="${detected_public_ip}"
fi
if [ -z "${server_ip}" ]; then
  server_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
fi
[ -n "${server_ip}" ] && validate_ip "${server_ip}" || fail "could not determine a valid server IP; rerun with --ip ADDRESS"
log_line "Server IP: ${server_ip}"

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

stage "Prepare TLS certificate"
if ! cert_matches_ip; then
  tmp_key="${tls_directory}/server.key.new.$$"
  tmp_cert="${tls_directory}/server.crt.new.$$"
  rm -f "${tmp_key}" "${tmp_cert}"
  openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 3650 \
    -keyout "${tmp_key}" -out "${tmp_cert}" -subj "/CN=${server_ip}" \
    -addext "subjectAltName=IP:${server_ip}" >> "${log_file}" 2>&1 || fail "could not generate self-signed fallback certificate"
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
    stage "Request trusted Let's Encrypt IP certificate"
    log_line "ACME challenge: ${challenge#--}"
    if run_live /usr/local/lib/egress-manager/bin/lego run \
      --accept-tos --path "${acme_directory}" --domains "${server_ip}" \
      --profile shortlived --key-type EC256 --no-random-sleep ${challenge}; then
      if find_acme_material; then
        install -o root -g root -m 0644 "${cert_source}" "${certificate_path}"
        install -o root -g egress-manager -m 0640 "${key_source}" "${private_key_path}"
        cert_mode="letsencrypt"
        log_line "Trusted certificate installed."
      else
        warn "ACME completed but matching certificate material was not found; keeping self-signed fallback"
      fi
    else
      warn "public certificate issuance failed; keeping the IP self-signed fallback"
    fi
  else
    warn "ports 80 and 443 are already occupied; keeping the IP self-signed fallback"
  fi
fi

printf '%s\n' "${cert_mode}" > "${mode_path}"
chown root:egress-manager "${mode_path}"
chmod 0640 "${mode_path}"

stage "Configure HTTPS"
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
  log_line "Egress Manager secure files installed; service start skipped."
  log_line "Install log: ${log_file}"
  exit 0
fi

stage "Start and verify services"
run_logged systemctl daemon-reload || fail "systemd daemon-reload failed"
run_logged systemctl enable egressd.service egress-web.service egress-manager-xray-relay.service || fail "could not enable Egress Manager services"
if [ "${cert_mode}" = "letsencrypt" ]; then
  run_logged systemctl enable --now egress-manager-cert-renew.timer || fail "could not enable certificate renewal timer"
else
  systemctl disable --now egress-manager-cert-renew.timer >> "${log_file}" 2>&1 || true
fi
run_logged systemctl restart egressd.service || fail "egressd failed to restart"
run_logged systemctl restart egress-web.service || fail "egress-web failed to restart"

panel_port="$(sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' /etc/egress-manager/config.json)"
[ -n "${panel_port}" ] || fail "cannot read panel port"
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if systemctl is-active --quiet egressd.service && systemctl is-active --quiet egress-web.service && curl -kfsS --max-time 2 "https://127.0.0.1:${panel_port}/api/v1/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "${ready}" != "1" ]; then
  run_live systemctl --no-pager --full status egressd.service egress-web.service || true
  fail "HTTPS services did not become healthy"
fi
log_line "Health check: OK"

prompt_admin=0
if [ "${admin_mode}" = "prompt" ]; then
  prompt_admin=1
elif [ "${admin_mode}" = "auto" ] && [ "${fresh_database}" = "1" ] && has_controlling_tty; then
  prompt_admin=1
fi

if [ "${prompt_admin}" = "1" ]; then
  stage "Create administrator"
  has_controlling_tty || fail "an interactive terminal is required to provision the administrator"
  if [ "${admin_mode}" = "auto" ]; then
    printf 'Administrator username [%s]: ' "${admin_user}" >/dev/tty
    IFS= read -r entered_user </dev/tty
    [ -n "${entered_user}" ] && admin_user="${entered_user}"
  fi
  printf 'Administrator password (minimum 12 characters): ' >/dev/tty
  stty -echo </dev/tty
  stty_hidden=1
  IFS= read -r admin_password </dev/tty
  stty echo </dev/tty
  stty_hidden=0
  printf '\nConfirm administrator password: ' >/dev/tty
  stty -echo </dev/tty
  stty_hidden=1
  IFS= read -r admin_password_confirm </dev/tty
  stty echo </dev/tty
  stty_hidden=0
  printf '\n' >/dev/tty
  [ "${admin_password}" = "${admin_password_confirm}" ] || fail "administrator passwords do not match"
  [ "$(LC_ALL=C printf '%s' "${admin_password}" | wc -c)" -ge 12 ] || fail "administrator password must be at least 12 bytes"
  printf '%s\n' "${admin_password}" | /usr/local/lib/egress-manager/bin/egress-web provision-admin --username "${admin_user}" --password-stdin >> "${log_file}" 2>&1 || fail "administrator provisioning failed"
  unset admin_password admin_password_confirm
  log_line "Administrator provisioned: ${admin_user}"
elif [ "${admin_mode}" = "auto" ] && [ "${fresh_database}" = "1" ]; then
  warn "administrator was not provisioned because no interactive terminal is available; run: sudo egress-manager admin operator"
fi

stage "Installation complete"
installed_version="$(sed -n '1p' "${bundle_root}/VERSION")"
log_line "Installed Egress Manager ${installed_version} with HTTPS."
log_line "Panel: https://${server_ip}:${panel_port}/login"
if [ "${cert_mode}" = "letsencrypt" ]; then
  log_line "TLS: trusted Let's Encrypt IP certificate; automatic renewal enabled."
else
  log_line "TLS: self-signed IP certificate (SAN=${server_ip}); browser trust warning is expected until this certificate is trusted."
fi
log_line "Management: sudo egress-manager"
log_line "Install log: ${log_file}"
