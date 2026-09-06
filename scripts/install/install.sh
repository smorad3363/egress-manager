#!/bin/sh
set -eu

repository="smorad3363/egress-manager"
version="${EGRESS_VERSION:-v0.1.0-alpha.6}"
bundle_root="${EGRESS_BUNDLE_ROOT:-}"
skip_start="${EGRESS_SKIP_START:-0}"
public_http="${EGRESS_PUBLIC_HTTP:-1}"

usage() {
  cat <<'EOF_USAGE'
Usage: install.sh [--version TAG] [--bundle-root DIR] [--skip-start] [--public-http|--local-only]

Online mode downloads one complete release bundle for this Ubuntu version and architecture.
Offline mode runs from an extracted bundle and performs no external network access.
By default the browser panel listens on 0.0.0.0 over plain HTTP for direct browser access.
Use --local-only to bind it to 127.0.0.1 instead.
Supported hosts: Ubuntu 22.04, 24.04, and 26.04 on amd64 or arm64 with systemd.
EOF_USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; version="$2"; shift 2 ;;
    --bundle-root) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; bundle_root="$2"; shift 2 ;;
    --skip-start) skip_start=1; shift ;;
    --public-http) public_http=1; shift ;;
    --local-only) public_http=0; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "install.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

fail() {
  echo "install.sh: $*" >&2
  exit 1
}

[ "$(id -u)" -eq 0 ] || fail "run as root (use sudo)"
[ "$(uname -s)" = "Linux" ] || fail "Linux is required"
[ -r /etc/os-release ] || fail "/etc/os-release is unavailable"
. /etc/os-release
[ "${ID:-}" = "ubuntu" ] || fail "supported distributions: Ubuntu 22.04, 24.04, 26.04"
case "${VERSION_ID:-}" in
  22.04|24.04|26.04) ubuntu_version="${VERSION_ID}" ;;
  *) fail "unsupported Ubuntu release: ${VERSION_ID:-unknown}" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) architecture="amd64" ;;
  aarch64|arm64) architecture="arm64" ;;
  *) fail "supported architectures: amd64, arm64" ;;
esac

for required in /proc/sys/net/ipv4/ip_forward /proc/net/route; do
  [ -e "${required}" ] || fail "required kernel networking interface missing: ${required}"
done

if [ "${skip_start}" != "1" ]; then
  command -v systemctl >/dev/null 2>&1 || fail "systemd is required"
  init_comm=""
  IFS= read -r init_comm < /proc/1/comm || true
  [ "${init_comm}" = "systemd" ] || fail "systemd must be PID 1"
fi

script_directory=""
case "$0" in
  /*|*/*) script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || true)" ;;
esac
if [ -z "${bundle_root}" ] && [ -n "${script_directory}" ] && [ -d "${script_directory}/package" ] && [ -d "${script_directory}/debs" ]; then
  bundle_root="${script_directory}"
fi

if [ -z "${bundle_root}" ]; then
  command -v curl >/dev/null 2>&1 || fail "curl is required for online bootstrap"
  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required for online bootstrap"
  if ! python3 -c 'import json, zipfile' >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends --no-remove python3 ca-certificates
  fi
  python3 -c 'import json, zipfile' >/dev/null 2>&1 || fail "a complete Python 3 standard library is required for the online bootstrap"

  temporary_directory="$(mktemp -d)"
  cleanup_bootstrap() { rm -rf -- "${temporary_directory}"; }
  trap cleanup_bootstrap EXIT HUP INT TERM
  bundle_name="egress-manager-offline-ubuntu${ubuntu_version}-${architecture}"
  release_base="https://github.com/${repository}/releases/download/${version}"
  archive="${temporary_directory}/${bundle_name}.zip"
  checksum="${archive}.sha256"
  echo "Downloading Egress Manager ${version} full bundle for Ubuntu ${ubuntu_version} ${architecture}..."
  curl --fail --location --silent --show-error --retry 3 --output "${archive}" "${release_base}/${bundle_name}.zip"
  curl --fail --location --silent --show-error --retry 3 --output "${checksum}" "${release_base}/${bundle_name}.zip.sha256"
  expected="$(awk '{print $1}' "${checksum}")"
  actual="$(sha256sum "${archive}" | awk '{print $1}')"
  [ -n "${expected}" ] && [ "${expected}" = "${actual}" ] || fail "release bundle checksum mismatch"
  python3 -m zipfile -e "${archive}" "${temporary_directory}/extracted"
  extracted_root="${temporary_directory}/extracted/${bundle_name}"
  [ -f "${extracted_root}/install.sh" ] || fail "release bundle is incomplete"
  mode_arg="--public-http"
  [ "${public_http}" = "1" ] || mode_arg="--local-only"
  if [ "${skip_start}" = "1" ]; then
    sh "${extracted_root}/install.sh" --bundle-root "${extracted_root}" --skip-start "${mode_arg}"
  else
    sh "${extracted_root}/install.sh" --bundle-root "${extracted_root}" "${mode_arg}"
  fi
  exit $?
fi

bundle_root="$(CDPATH= cd -- "${bundle_root}" 2>/dev/null && pwd)" || fail "bundle root does not exist"
[ -f "${bundle_root}/UBUNTU_VERSION" ] || fail "offline bundle has no UBUNTU_VERSION"
[ -f "${bundle_root}/ARCHITECTURE" ] || fail "offline bundle has no ARCHITECTURE"
[ -f "${bundle_root}/VERSION" ] || fail "offline bundle has no VERSION"
[ -f "${bundle_root}/DEPENDENCY_ROOTS" ] || fail "offline bundle has no DEPENDENCY_ROOTS"
[ -s "${bundle_root}/debs/Packages" ] || fail "offline bundle has no local APT package index"
[ -f "${bundle_root}/MANIFEST.sha256" ] || fail "offline bundle has no MANIFEST.sha256"
[ "$(sed -n '1p' "${bundle_root}/UBUNTU_VERSION")" = "${ubuntu_version}" ] || fail "offline bundle is for a different Ubuntu release"
[ "$(sed -n '1p' "${bundle_root}/ARCHITECTURE")" = "${architecture}" ] || fail "offline bundle is for a different architecture"
installed_version="$(sed -n '1p' "${bundle_root}/VERSION")"
[ -n "${installed_version}" ] || fail "offline bundle has no version"

(
  cd "${bundle_root}"
  sha256sum --check --status MANIFEST.sha256
) || fail "offline bundle manifest verification failed"

package_directory="${bundle_root}/package"
for file in \
  bin/egressd bin/egress-web bin/egressctl bin/sing-box bin/xray \
  config.json.in verify.sh manager.sh VERSION RUNTIME_VERSIONS web/index.html \
  systemd/egressd.service systemd/egress-web.service systemd/egress-manager-sing-box.service; do
  [ -f "${package_directory}/${file}" ] || fail "release bundle is incomplete: ${file}"
done
[ "$(sed -n '1p' "${package_directory}/VERSION")" = "${installed_version}" ] || fail "package and bundle versions differ"

command -v apt-get >/dev/null 2>&1 || fail "apt-get is required on the base Ubuntu installation"
command -v dpkg >/dev/null 2>&1 || fail "dpkg is required on the base Ubuntu installation"
export DEBIAN_FRONTEND=noninteractive

# The bundle is an indexed local APT repository. Request only the runtime roots instead of
# forcing every bundled .deb as a top-level package. Already-installed Ubuntu packages can
# therefore satisfy dependencies at their current versions. Repository access is disabled.
# --no-upgrade keeps existing root packages at their installed versions; --no-remove makes
# package preservation a hard invariant.
set --
while IFS= read -r dependency; do
  case "${dependency}" in ''|'#'*) continue ;; esac
  set -- "$@" "${dependency}"
done < "${bundle_root}/DEPENDENCY_ROOTS"
[ "$#" -gt 0 ] || fail "offline dependency root set is empty"

apt_directory="$(mktemp -d)"
cleanup_apt() { rm -rf -- "${apt_directory}"; }
trap cleanup_apt EXIT HUP INT TERM
mkdir -p "${apt_directory}/lists/partial" "${apt_directory}/cache/archives/partial"
sources_file="${apt_directory}/sources.list"
printf 'deb [trusted=yes] file:%s/debs ./\n' "${bundle_root}" > "${sources_file}"

apt_local() {
  apt-get \
    -o "Dir::Etc::sourcelist=${sources_file}" \
    -o Dir::Etc::sourceparts=- \
    -o "Dir::State::lists=${apt_directory}/lists" \
    -o "Dir::Cache::archives=${apt_directory}/cache/archives" \
    -o APT::Get::List-Cleanup=0 \
    -o APT::Sandbox::User=root \
    "$@"
}

apt_local update >/dev/null
apt_local install -s --no-install-recommends --no-upgrade --no-remove "$@" >/dev/null || fail "offline dependency plan would upgrade, downgrade, remove, or conflict with existing host packages; no package changes were made"
apt_local install -y --no-install-recommends --no-upgrade --no-remove "$@"
cleanup_apt
trap - EXIT HUP INT TERM

for command in curl tar gzip sha256sum install getent useradd groupadd sed od awk ss shuf tr ps grep readlink sort paste find nft iptables ip haproxy wg openvpn python3; do
  command -v "${command}" >/dev/null 2>&1 || fail "required command missing after offline dependency install: ${command}"
done
python3 -c 'import json, zipfile' >/dev/null 2>&1 || fail "offline dependency install did not provide a complete Python 3 standard library"

if [ -e /usr/local/bin/egressctl ] && { [ ! -L /usr/local/bin/egressctl ] || [ "$(readlink /usr/local/bin/egressctl)" != "/usr/local/lib/egress-manager/bin/egressctl" ]; }; then
  fail "/usr/local/bin/egressctl already exists and is not owned by Egress Manager"
fi
if [ -e /usr/local/bin/egress-manager ] && { [ ! -L /usr/local/bin/egress-manager ] || [ "$(readlink /usr/local/bin/egress-manager)" != "/usr/local/lib/egress-manager/manager.sh" ]; }; then
  fail "/usr/local/bin/egress-manager already exists and is not owned by Egress Manager"
fi

for protected_path in \
  /etc/egress-manager /etc/egress-manager/config.json /etc/egress-manager/ipc.key \
  /var/lib/egress-manager /var/lib/egress-manager/private /var/lib/egress-manager/database \
  /usr/local/lib/egress-manager /usr/local/lib/egress-manager/bin /usr/local/lib/egress-manager/web \
  /usr/local/lib/egress-manager/share /usr/local/lib/egress-manager/share/xray; do
  [ ! -L "${protected_path}" ] || fail "refusing symbolic link at protected path: ${protected_path}"
done

if [ -e /etc/egress-manager/config.json ]; then
  grep -q '"database_path":[[:space:]]*"/var/lib/egress-manager/database/egress-manager.db"' /etc/egress-manager/config.json || fail "existing configuration uses a custom database path; migrate it before upgrading"
fi

if ! getent group egress-manager >/dev/null 2>&1; then
  groupadd --system egress-manager
fi
if ! getent passwd egress-web >/dev/null 2>&1; then
  useradd --system --gid egress-manager --home-dir /var/lib/egress-manager --no-create-home --shell /usr/sbin/nologin egress-web
fi
[ "$(id -u egress-web)" != "0" ] || fail "egress-web must not use UID 0"
[ "$(getent group egress-manager | awk -F: '{print $3}')" != "0" ] || fail "egress-manager must not use GID 0"

install -d -o root -g root -m 0755 /usr/local/lib/egress-manager /usr/local/lib/egress-manager/bin /usr/local/lib/egress-manager/share /usr/local/lib/egress-manager/share/xray
for command in egressd egress-web egressctl sing-box xray; do
  install -o root -g root -m 0755 "${package_directory}/bin/${command}" "/usr/local/lib/egress-manager/bin/${command}"
done
for asset in geoip.dat geosite.dat; do
  if [ -f "${package_directory}/share/xray/${asset}" ]; then
    install -o root -g root -m 0644 "${package_directory}/share/xray/${asset}" "/usr/local/lib/egress-manager/share/xray/${asset}"
  fi
done
/usr/local/lib/egress-manager/bin/egressd --version >/dev/null
/usr/local/lib/egress-manager/bin/egress-web --version >/dev/null
/usr/local/lib/egress-manager/bin/egressctl --version >/dev/null
/usr/local/lib/egress-manager/bin/sing-box version >/dev/null
/usr/local/lib/egress-manager/bin/xray version >/dev/null
install -o root -g root -m 0755 "${package_directory}/manager.sh" /usr/local/lib/egress-manager/manager.sh
ln -sfn /usr/local/lib/egress-manager/bin/egressctl /usr/local/bin/egressctl
ln -sfn /usr/local/lib/egress-manager/manager.sh /usr/local/bin/egress-manager

web_stage="/usr/local/lib/egress-manager/web.new.$$"
rm -rf -- "${web_stage}"
install -d -o root -g root -m 0755 "${web_stage}"
cp -R "${package_directory}/web/." "${web_stage}/"
find "${web_stage}" -type d -exec chmod 0755 {} \;
find "${web_stage}" -type f -exec chmod 0644 {} \;
chown -R root:root "${web_stage}"
rm -rf -- /usr/local/lib/egress-manager/web
mv "${web_stage}" /usr/local/lib/egress-manager/web

install -d -o root -g egress-manager -m 0750 /etc/egress-manager
install -d -o root -g egress-manager -m 0750 /var/lib/egress-manager
install -d -o root -g root -m 0700 /var/lib/egress-manager/private
install -d -o root -g egress-manager -m 0770 /var/lib/egress-manager/database

if [ ! -e /etc/egress-manager/ipc.key ]; then
  umask 007
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > /etc/egress-manager/ipc.key
fi
chown root:egress-manager /etc/egress-manager/ipc.key
chmod 0640 /etc/egress-manager/ipc.key

temporary_directory="$(mktemp -d)"
cleanup_install() { rm -rf -- "${temporary_directory}"; }
trap cleanup_install EXIT HUP INT TERM

if [ ! -e /etc/egress-manager/config.json ]; then
  panel_port=""
  for candidate in $(shuf -i 43127-49151 -n 512); do
    if ! ss -H -ltn | awk -v suffix=":${candidate}" 'substr($4, length($4)-length(suffix)+1) == suffix { found=1 } END { exit !found }'; then
      panel_port="${candidate}"
      break
    fi
  done
  [ -n "${panel_port}" ] || fail "no free panel port found"

  printf '22\n' > "${temporary_directory}/ssh-ports"
  ss -H -ltnp 2>/dev/null | awk '/sshd/ { address=$4; sub(/^.*:/, "", address); if (address ~ /^[0-9]+$/) print address }' >> "${temporary_directory}/ssh-ports"
  ssh_ports="$(sort -nu "${temporary_directory}/ssh-ports" | paste -sd, -)"
  sed -e "s/@@PANEL_PORT@@/${panel_port}/" -e "s/@@SSH_PORTS@@/${ssh_ports}/" "${package_directory}/config.json.in" > "${temporary_directory}/config.json"
  install -o root -g egress-manager -m 0640 "${temporary_directory}/config.json" /etc/egress-manager/config.json
fi

configure_http_mode() {
  tmp="${temporary_directory}/config-http.json"
  if [ "${public_http}" = "1" ]; then
    sed 's/^[[:space:]]*"listen_address":[[:space:]]*"[^"]*"/  "listen_address": "0.0.0.0"/' /etc/egress-manager/config.json > "${tmp}"
    if grep -q '^[[:space:]]*"allow_insecure_http"[[:space:]]*:' "${tmp}"; then
      sed 's/^[[:space:]]*"allow_insecure_http":[[:space:]]*[^,]*/  "allow_insecure_http": true/' "${tmp}" > "${tmp}.2"
      mv "${tmp}.2" "${tmp}"
    else
      awk '{ print; if ($0 ~ /^[[:space:]]*"listen_port"[[:space:]]*:/) print "  \"allow_insecure_http\": true," }' "${tmp}" > "${tmp}.2"
      mv "${tmp}.2" "${tmp}"
    fi
  else
    sed 's/^[[:space:]]*"listen_address":[[:space:]]*"[^"]*"/  "listen_address": "127.0.0.1"/' /etc/egress-manager/config.json > "${tmp}"
    if grep -q '^[[:space:]]*"allow_insecure_http"[[:space:]]*:' "${tmp}"; then
      sed 's/^[[:space:]]*"allow_insecure_http":[[:space:]]*[^,]*/  "allow_insecure_http": false/' "${tmp}" > "${tmp}.2"
      mv "${tmp}.2" "${tmp}"
    fi
  fi
  install -o root -g egress-manager -m 0640 "${tmp}" /etc/egress-manager/config.json
}
configure_http_mode

install -o root -g root -m 0644 "${package_directory}/systemd/egressd.service" /etc/systemd/system/egressd.service
install -o root -g root -m 0644 "${package_directory}/systemd/egress-web.service" /etc/systemd/system/egress-web.service
install -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-sing-box.service" /etc/systemd/system/egress-manager-sing-box.service
install -o root -g root -m 0755 "${package_directory}/verify.sh" /usr/local/lib/egress-manager/verify.sh
install -o root -g root -m 0644 "${package_directory}/RUNTIME_VERSIONS" /usr/local/lib/egress-manager/RUNTIME_VERSIONS

if [ "${skip_start}" = "1" ]; then
  printf 'Egress Manager %s files installed; service start skipped.\n' "${installed_version}"
  exit 0
fi

systemctl daemon-reload
systemctl enable egressd.service egress-web.service
systemctl restart egressd.service
systemctl restart egress-web.service

panel_port="$(sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' /etc/egress-manager/config.json)"
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if systemctl is-active --quiet egressd.service && systemctl is-active --quiet egress-web.service && curl --fail --silent --max-time 2 "http://127.0.0.1:${panel_port}/api/v1/health" >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done

if [ "${ready}" != "1" ]; then
  systemctl --no-pager --full status egressd.service egress-web.service >&2 || true
  journalctl --no-pager --lines=30 --unit=egressd.service --unit=egress-web.service >&2 || true
  fail "services did not become healthy"
fi

/usr/local/lib/egress-manager/verify.sh

printf 'Installed Egress Manager %s.\n' "${installed_version}"
if [ "${public_http}" = "1" ]; then
  server_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
  [ -n "${server_ip}" ] || server_ip="SERVER_IP"
  printf 'Panel: http://%s:%s/login\n' "${server_ip}" "${panel_port}"
  printf 'WARNING: public HTTP mode is enabled; login credentials and session traffic are not encrypted.\n'
  printf 'If a host firewall blocks the port, run: sudo egress-manager firewall-open\n'
else
  printf 'Panel (local only): http://127.0.0.1:%s/login\n' "${panel_port}"
fi
printf 'Management: sudo egress-manager\n'
printf 'Xray is bundled for Egress Manager validation/integration and is not enabled as a standalone system service.\n'
