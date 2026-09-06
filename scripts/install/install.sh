#!/bin/sh
set -eu

repository="smorad3363/egress-manager"
version="${EGRESS_VERSION:-v0.1.0-alpha.1}"
artifact_file="${EGRESS_ARTIFACT_FILE:-}"
skip_dependencies="${EGRESS_SKIP_DEPENDENCIES:-0}"
skip_start="${EGRESS_SKIP_START:-0}"
sing_box_version="1.13.20"

usage() {
  cat <<'EOF'
Usage: install.sh [--version TAG] [--artifact FILE] [--skip-start]

Installs Egress Manager on Ubuntu 22.04, 24.04, or 26.04 (amd64/arm64).
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; version="$2"; shift 2 ;;
    --artifact) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; artifact_file="$2"; shift 2 ;;
    --skip-start) skip_start=1; shift ;;
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
  22.04|24.04|26.04) ;;
  *) fail "unsupported Ubuntu release: ${VERSION_ID:-unknown}" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)
    architecture="amd64"
    sing_box_sha256="646bc01bf128c32a12eb50d8690e387bba7504da7b1d65c704bd53916e38595a"
    ;;
  aarch64|arm64)
    architecture="arm64"
    sing_box_sha256="7f8187b1d1d30258cd4fa70892eaa232649f8f28b294078eeac719579e14cf42"
    ;;
  *) fail "supported architectures: amd64, arm64" ;;
esac

for required in /proc/sys/net/ipv4/ip_forward /proc/net/route; do
  [ -e "${required}" ] || fail "required kernel networking interface missing: ${required}"
done

if [ "${skip_start}" != "1" ]; then
  command -v systemctl >/dev/null 2>&1 || fail "systemd is required"
  [ "$(ps -p 1 -o comm= 2>/dev/null | tr -d ' ')" = "systemd" ] || fail "systemd must be PID 1"
fi

if [ -e /usr/local/bin/egressctl ] && { [ ! -L /usr/local/bin/egressctl ] || [ "$(readlink /usr/local/bin/egressctl)" != "/usr/local/lib/egress-manager/bin/egressctl" ]; }; then
  fail "/usr/local/bin/egressctl already exists and is not owned by Egress Manager"
fi

for protected_path in /etc/egress-manager /etc/egress-manager/config.json /etc/egress-manager/ipc.key /var/lib/egress-manager /var/lib/egress-manager/private /var/lib/egress-manager/database /usr/local/lib/egress-manager; do
  [ ! -L "${protected_path}" ] || fail "refusing symbolic link at protected path: ${protected_path}"
done

if [ -e /etc/egress-manager/config.json ]; then
  grep -q '"database_path":[[:space:]]*"/var/lib/egress-manager/database/egress-manager.db"' /etc/egress-manager/config.json || fail "existing configuration uses a custom database path; migrate it before upgrading"
fi

if [ "${skip_dependencies}" != "1" ]; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends ca-certificates curl tar gzip coreutils iproute2 nftables iptables haproxy wireguard-tools openvpn
fi

for command in curl tar sha256sum install getent useradd groupadd sed od awk ss shuf tr ps grep readlink sort paste nft iptables ip haproxy wg openvpn; do
  command -v "${command}" >/dev/null 2>&1 || fail "required command missing: ${command}"
done

temporary_directory="$(mktemp -d)"
cleanup() { rm -rf -- "${temporary_directory}"; }
trap cleanup EXIT HUP INT TERM

if [ -n "${artifact_file}" ]; then
  [ -f "${artifact_file}" ] || fail "artifact does not exist: ${artifact_file}"
  cp "${artifact_file}" "${temporary_directory}/package.tar.gz"
else
  release_base="https://github.com/${repository}/releases/download/${version}"
  artifact_name="egress-manager-linux-${architecture}.tar.gz"
  curl --fail --location --silent --show-error --retry 3 --output "${temporary_directory}/package.tar.gz" "${release_base}/${artifact_name}"
  curl --fail --location --silent --show-error --retry 3 --output "${temporary_directory}/package.sha256" "${release_base}/${artifact_name}.sha256"
  expected="$(awk '{print $1}' "${temporary_directory}/package.sha256")"
  actual="$(sha256sum "${temporary_directory}/package.tar.gz" | awk '{print $1}')"
  [ "${expected}" = "${actual}" ] || fail "release artifact checksum mismatch"
fi

tar -xzf "${temporary_directory}/package.tar.gz" -C "${temporary_directory}"
package_directory="${temporary_directory}/egress-manager-linux-${architecture}"
for file in bin/egressd bin/egress-web bin/egressctl config.json.in verify.sh VERSION systemd/egressd.service systemd/egress-web.service systemd/egress-manager-sing-box.service; do
  [ -f "${package_directory}/${file}" ] || fail "release artifact is incomplete: ${file}"
done
installed_version="$(sed -n '1p' "${package_directory}/VERSION")"
[ -n "${installed_version}" ] || fail "release artifact has no version"
if [ -z "${artifact_file}" ] && [ "${installed_version}" != "${version}" ]; then
  fail "release artifact version mismatch: expected ${version}, got ${installed_version}"
fi

sing_box_archive="sing-box-${sing_box_version}-linux-${architecture}.tar.gz"
curl --fail --location --silent --show-error --retry 3 --output "${temporary_directory}/${sing_box_archive}" "https://github.com/SagerNet/sing-box/releases/download/v${sing_box_version}/${sing_box_archive}"
printf '%s  %s\n' "${sing_box_sha256}" "${temporary_directory}/${sing_box_archive}" | sha256sum --check --status || fail "sing-box checksum mismatch"
tar -xzf "${temporary_directory}/${sing_box_archive}" -C "${temporary_directory}"
sing_box_binary="${temporary_directory}/sing-box-${sing_box_version}-linux-${architecture}/sing-box"
[ -f "${sing_box_binary}" ] || fail "sing-box archive is incomplete"

if ! getent group egress-manager >/dev/null 2>&1; then
  groupadd --system egress-manager
fi
if ! getent passwd egress-web >/dev/null 2>&1; then
  useradd --system --gid egress-manager --home-dir /var/lib/egress-manager --no-create-home --shell /usr/sbin/nologin egress-web
fi
[ "$(id -u egress-web)" != "0" ] || fail "egress-web must not use UID 0"
[ "$(getent group egress-manager | awk -F: '{print $3}')" != "0" ] || fail "egress-manager must not use GID 0"

install -d -o root -g root -m 0755 /usr/local/lib/egress-manager /usr/local/lib/egress-manager/bin
install -o root -g root -m 0755 "${package_directory}/bin/egressd" /usr/local/lib/egress-manager/bin/egressd
install -o root -g root -m 0755 "${package_directory}/bin/egress-web" /usr/local/lib/egress-manager/bin/egress-web
install -o root -g root -m 0755 "${package_directory}/bin/egressctl" /usr/local/lib/egress-manager/bin/egressctl
install -o root -g root -m 0755 "${sing_box_binary}" /usr/local/lib/egress-manager/bin/sing-box
/usr/local/lib/egress-manager/bin/egressd --version >/dev/null
/usr/local/lib/egress-manager/bin/egress-web --version >/dev/null
/usr/local/lib/egress-manager/bin/egressctl --version >/dev/null
/usr/local/lib/egress-manager/bin/sing-box version >/dev/null
ln -sfn /usr/local/lib/egress-manager/bin/egressctl /usr/local/bin/egressctl

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

install -o root -g root -m 0644 "${package_directory}/systemd/egressd.service" /etc/systemd/system/egressd.service
install -o root -g root -m 0644 "${package_directory}/systemd/egress-web.service" /etc/systemd/system/egress-web.service
install -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-sing-box.service" /etc/systemd/system/egress-manager-sing-box.service
install -o root -g root -m 0755 "${package_directory}/verify.sh" /usr/local/lib/egress-manager/verify.sh

if [ "${skip_start}" = "1" ]; then
  echo "Egress Manager files installed; service start skipped."
  exit 0
fi

systemctl daemon-reload
systemctl enable egressd.service egress-web.service
systemctl restart egressd.service
systemctl restart egress-web.service

for attempt in 1 2 3 4 5 6 7 8 9 10; do
  if systemctl is-active --quiet egressd.service && systemctl is-active --quiet egress-web.service; then
    break
  fi
  sleep 1
done

if ! systemctl is-active --quiet egressd.service || ! systemctl is-active --quiet egress-web.service; then
  systemctl --no-pager --full status egressd.service egress-web.service >&2 || true
  fail "services did not become active"
fi

panel_port="$(sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' /etc/egress-manager/config.json)"
curl --fail --silent --show-error --max-time 10 "http://127.0.0.1:${panel_port}/api/v1/health" >/dev/null || fail "health check failed"
/usr/local/lib/egress-manager/bin/egressctl status --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key

printf 'Installed Egress Manager %s.\n' "${installed_version}"
printf 'Local API: http://127.0.0.1:%s\n' "${panel_port}"
printf 'Next: provision an administrator using scripts/install/README.md.\n'
