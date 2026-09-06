#!/bin/sh
set -eu

[ "$(uname -s)" = "Linux" ] || { echo "build-offline-bundle.sh: Linux is required" >&2; exit 1; }

ubuntu_version="${UBUNTU_VERSION:-22.04}"
architecture="${ARCHITECTURE:-amd64}"
output_directory="${OUTPUT_DIRECTORY:-dist}"
version="${VERSION:-dev}"

case "${ubuntu_version}" in 22.04|24.04|26.04) ;; *) echo "build-offline-bundle.sh: unsupported Ubuntu version: ${ubuntu_version}" >&2; exit 1 ;; esac
case "${architecture}" in amd64|arm64) ;; *) echo "build-offline-bundle.sh: unsupported architecture: ${architecture}" >&2; exit 1 ;; esac

for required in docker zip tar sha256sum cp; do
  command -v "${required}" >/dev/null 2>&1 || { echo "build-offline-bundle.sh: missing command: ${required}" >&2; exit 1; }
done

package_directory="${output_directory}/egress-manager-linux-${architecture}"
[ -f "${package_directory}/VERSION" ] || { echo "build-offline-bundle.sh: build ${package_directory} first" >&2; exit 1; }
package_version="$(sed -n '1p' "${package_directory}/VERSION")"
[ "${version}" = "dev" ] || [ "${package_version}" = "${version}" ] || { echo "build-offline-bundle.sh: package version mismatch" >&2; exit 1; }

bundle_name="egress-manager-offline-ubuntu${ubuntu_version}-${architecture}"
bundle_directory="${output_directory}/${bundle_name}"
zip_archive="${output_directory}/${bundle_name}.zip"
tar_archive="${output_directory}/${bundle_name}.tar.gz"
rm -rf "${bundle_directory}" "${zip_archive}" "${zip_archive}.sha256" "${tar_archive}" "${tar_archive}.sha256"
mkdir -p "${bundle_directory}/package" "${bundle_directory}/debs"
cp -R "${package_directory}/." "${bundle_directory}/package/"
cp scripts/install/install.sh "${bundle_directory}/install-core.sh"
cp scripts/install/install-secure.sh "${bundle_directory}/install.sh"
chmod 0755 "${bundle_directory}/install.sh" "${bundle_directory}/install-core.sh"
printf '%s\n' "${ubuntu_version}" > "${bundle_directory}/UBUNTU_VERSION"
printf '%s\n' "${architecture}" > "${bundle_directory}/ARCHITECTURE"
printf '%s\n' "${package_version}" > "${bundle_directory}/VERSION"
cat > "${bundle_directory}/DEPENDENCY_ROOTS" <<'EOF_ROOTS'
ca-certificates
curl
tar
gzip
coreutils
grep
sed
mawk
findutils
iproute2
nftables
iptables
haproxy
wireguard-tools
openvpn
procps
passwd
util-linux
openssl
python3
EOF_ROOTS

bundle_absolute="$(cd "${bundle_directory}" && pwd)"
docker run --rm --platform "linux/${architecture}" \
  --volume "${bundle_absolute}/debs:/bundle" \
  "ubuntu:${ubuntu_version}" sh -eu -c '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends dpkg-dev
    mkdir -p /bundle/partial
    chmod 1777 /bundle /bundle/partial
    touch /tmp/empty-status
    apt-get \
      -o Dir::State::status=/tmp/empty-status \
      -o Dir::Cache::archives=/bundle \
      -o APT::Get::Download-Only=true \
      install -y --no-install-recommends \
        ca-certificates curl tar gzip coreutils grep sed mawk findutils \
        iproute2 nftables iptables haproxy wireguard-tools openvpn \
        procps passwd util-linux openssl python3
    rm -rf /bundle/partial /bundle/lock
    cd /bundle
    dpkg-scanpackages . /dev/null > Packages
  '

find "${bundle_directory}/debs" -type f -name '*.deb' | grep -q . || { echo "build-offline-bundle.sh: dependency bundle is empty" >&2; exit 1; }
[ -s "${bundle_directory}/debs/Packages" ] || { echo "build-offline-bundle.sh: local APT index is empty" >&2; exit 1; }
(
  cd "${bundle_directory}"
  find . -type f ! -name MANIFEST.sha256 -print | LC_ALL=C sort | sed 's#^./##' | while IFS= read -r file; do
    sha256sum "${file}"
  done > MANIFEST.sha256
)

(
  cd "${output_directory}"
  zip -q -r "${bundle_name}.zip" "${bundle_name}"
  tar -czf "${bundle_name}.tar.gz" "${bundle_name}"
)
sha256sum "${zip_archive}" > "${zip_archive}.sha256"
sha256sum "${tar_archive}" > "${tar_archive}.sha256"
printf '%s\n%s\n' "${zip_archive}" "${tar_archive}"
