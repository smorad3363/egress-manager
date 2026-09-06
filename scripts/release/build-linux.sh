#!/bin/sh
set -eu

[ "$(uname -s)" = "Linux" ] || { echo "build-linux.sh: Linux is required" >&2; exit 1; }

case "${GOARCH:-$(go env GOARCH)}" in
  amd64|arm64) architecture="${GOARCH:-$(go env GOARCH)}" ;;
  *) echo "build-linux.sh: supported GOARCH values are amd64 and arm64" >&2; exit 1 ;;
esac

version="${VERSION:-dev}"
commit="${COMMIT:-$(git rev-parse --verify HEAD 2>/dev/null || printf unknown)}"
build_date="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
output_directory="${OUTPUT_DIRECTORY:-dist}"
package_directory="${output_directory}/egress-manager-linux-${architecture}"
archive="${output_directory}/egress-manager-linux-${architecture}.tar.gz"
ldflags="-s -w -X github.com/egress-manager/egress-manager/internal/buildinfo.Version=${version} -X github.com/egress-manager/egress-manager/internal/buildinfo.Commit=${commit} -X github.com/egress-manager/egress-manager/internal/buildinfo.Date=${build_date}"

rm -rf "${package_directory}"
mkdir -p "${package_directory}/bin" "${package_directory}/systemd"
for command in egressd egress-web egressctl; do
  CGO_ENABLED=1 GOOS=linux GOARCH="${architecture}" go build -trimpath -ldflags "${ldflags}" -o "${package_directory}/bin/${command}" "./cmd/${command}"
done
cp packaging/config.json.in "${package_directory}/config.json.in"
cp packaging/systemd/*.service "${package_directory}/systemd/"
cp scripts/install/verify.sh "${package_directory}/verify.sh"
chmod 0755 "${package_directory}/verify.sh"
printf '%s\n' "${version}" > "${package_directory}/VERSION"
tar -C "${output_directory}" -czf "${archive}" "egress-manager-linux-${architecture}"
sha256sum "${archive}" > "${archive}.sha256"
printf '%s\n' "${archive}"
