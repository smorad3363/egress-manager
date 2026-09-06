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
sing_box_version="1.13.20"
xray_version="26.7.28"
lego_version="5.3.1"

case "${architecture}" in
  amd64)
    sing_box_sha256="646bc01bf128c32a12eb50d8690e387bba7504da7b1d65c704bd53916e38595a"
    xray_archive="Xray-linux-64.zip"
    xray_sha256="8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40"
    lego_sha256="b3c71b122ee1947eacfe0b809b955647f6377239fe4bfc49f73b1a091ae1252a"
    ;;
  arm64)
    sing_box_sha256="7f8187b1d1d30258cd4fa70892eaa232649f8f28b294078eeac719579e14cf42"
    xray_archive="Xray-linux-arm64-v8a.zip"
    xray_sha256="f5698bb218ada3b4022db26fafc39601c5f53b46b19eb76c9616325985807501"
    lego_sha256="58db563a2b97c2259516fa9910b4a9e1634a0737723d0381a65af1bf93a4b433"
    ;;
esac

for required in go curl tar unzip sha256sum install; do
  command -v "${required}" >/dev/null 2>&1 || { echo "build-linux.sh: missing build command: ${required}" >&2; exit 1; }
done
[ -f web/dist/index.html ] || { echo "build-linux.sh: web/dist is missing; build the frontend first" >&2; exit 1; }

rm -rf "${package_directory}"
mkdir -p "${package_directory}/bin" "${package_directory}/systemd" "${package_directory}/web" "${package_directory}/share/xray" "${package_directory}/share/source" "${package_directory}/share/licenses"
for command in egressd egress-web egressctl; do
  CGO_ENABLED=1 GOOS=linux GOARCH="${architecture}" go build -trimpath -ldflags "${ldflags}" -o "${package_directory}/bin/${command}" "./cmd/${command}"
done

runtime_directory="$(mktemp -d)"
cleanup() { rm -rf -- "${runtime_directory}"; }
trap cleanup EXIT HUP INT TERM

sing_box_archive="sing-box-${sing_box_version}-linux-${architecture}.tar.gz"
curl --fail --location --silent --show-error --retry 3 --output "${runtime_directory}/${sing_box_archive}" "https://github.com/SagerNet/sing-box/releases/download/v${sing_box_version}/${sing_box_archive}"
printf '%s  %s\n' "${sing_box_sha256}" "${runtime_directory}/${sing_box_archive}" | sha256sum --check --status || { echo "build-linux.sh: sing-box checksum mismatch" >&2; exit 1; }
tar -xzf "${runtime_directory}/${sing_box_archive}" -C "${runtime_directory}"
install -m 0755 "${runtime_directory}/sing-box-${sing_box_version}-linux-${architecture}/sing-box" "${package_directory}/bin/sing-box"
curl --fail --location --silent --show-error --retry 3 --output "${package_directory}/share/source/sing-box-${sing_box_version}.tar.gz" "https://github.com/SagerNet/sing-box/archive/refs/tags/v${sing_box_version}.tar.gz"
curl --fail --location --silent --show-error --retry 3 --output "${package_directory}/share/licenses/sing-box-LICENSE" "https://raw.githubusercontent.com/SagerNet/sing-box/v${sing_box_version}/LICENSE"

curl --fail --location --silent --show-error --retry 3 --output "${runtime_directory}/${xray_archive}" "https://github.com/XTLS/Xray-core/releases/download/v${xray_version}/${xray_archive}"
printf '%s  %s\n' "${xray_sha256}" "${runtime_directory}/${xray_archive}" | sha256sum --check --status || { echo "build-linux.sh: Xray checksum mismatch" >&2; exit 1; }
mkdir -p "${runtime_directory}/xray"
unzip -q "${runtime_directory}/${xray_archive}" -d "${runtime_directory}/xray"
install -m 0755 "${runtime_directory}/xray/xray" "${package_directory}/bin/xray"
for asset in geoip.dat geosite.dat; do
  if [ -f "${runtime_directory}/xray/${asset}" ]; then
    install -m 0644 "${runtime_directory}/xray/${asset}" "${package_directory}/share/xray/${asset}"
  fi
done
curl --fail --location --silent --show-error --retry 3 --output "${package_directory}/share/source/xray-core-${xray_version}.tar.gz" "https://github.com/XTLS/Xray-core/archive/refs/tags/v${xray_version}.tar.gz"
curl --fail --location --silent --show-error --retry 3 --output "${package_directory}/share/licenses/xray-core-LICENSE" "https://raw.githubusercontent.com/XTLS/Xray-core/v${xray_version}/LICENSE"

lego_archive="lego_v${lego_version}_linux_${architecture}.tar.gz"
curl --fail --location --silent --show-error --retry 3 --output "${runtime_directory}/${lego_archive}" "https://github.com/go-acme/lego/releases/download/v${lego_version}/${lego_archive}"
printf '%s  %s\n' "${lego_sha256}" "${runtime_directory}/${lego_archive}" | sha256sum --check --status || { echo "build-linux.sh: lego checksum mismatch" >&2; exit 1; }
mkdir -p "${runtime_directory}/lego"
tar -xzf "${runtime_directory}/${lego_archive}" -C "${runtime_directory}/lego"
install -m 0755 "${runtime_directory}/lego/lego" "${package_directory}/bin/lego"
curl --fail --location --silent --show-error --retry 3 --output "${package_directory}/share/source/lego-${lego_version}.tar.gz" "https://github.com/go-acme/lego/archive/refs/tags/v${lego_version}.tar.gz"
curl --fail --location --silent --show-error --retry 3 --output "${package_directory}/share/licenses/lego-LICENSE" "https://raw.githubusercontent.com/go-acme/lego/v${lego_version}/LICENSE"

cp -R web/dist/. "${package_directory}/web/"
cp packaging/config.json.in "${package_directory}/config.json.in"
cp packaging/systemd/*.service "${package_directory}/systemd/"
for timer in packaging/systemd/*.timer; do [ -f "${timer}" ] && cp "${timer}" "${package_directory}/systemd/"; done
cp scripts/install/verify.sh "${package_directory}/verify.sh"
cp scripts/install/manager.sh "${package_directory}/manager.sh"
cp scripts/install/tls-renew.sh "${package_directory}/tls-renew.sh"
chmod 0755 "${package_directory}/verify.sh" "${package_directory}/manager.sh" "${package_directory}/tls-renew.sh"
printf '%s\n' "${version}" > "${package_directory}/VERSION"
cat > "${package_directory}/RUNTIME_VERSIONS" <<EOF_VERSIONS
sing-box=${sing_box_version}
xray=${xray_version}
lego=${lego_version}
frontend_node_build=24.19.0
frontend_pnpm_build=11.25.0
EOF_VERSIONS

tar -C "${output_directory}" -czf "${archive}" "egress-manager-linux-${architecture}"
sha256sum "${archive}" > "${archive}.sha256"
printf '%s\n' "${archive}"
