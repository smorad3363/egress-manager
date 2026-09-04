FROM ghcr.io/sagernet/sing-box:v1.13.20@sha256:83d7268e2a93127cc9c0ce03e845e68e247ab51b2cb41713fdfef2cee4d0b09e AS singbox

FROM golang:1.27.1 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY tests/network/inventoryprobe ./tests/network/inventoryprobe
COPY tests/network/natprobe ./tests/network/natprobe
COPY tests/network/counterprobe ./tests/network/counterprobe
COPY tests/network/iptablesprobe ./tests/network/iptablesprobe
COPY tests/network/haproxyprobe ./tests/network/haproxyprobe
COPY tests/network/singboxprobe ./tests/network/singboxprobe
COPY tests/network/routingprobe ./tests/network/routingprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-inventory-probe ./tests/network/inventoryprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-nat-probe ./tests/network/natprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-counter-probe ./tests/network/counterprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-iptables-probe ./tests/network/iptablesprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-haproxy-probe ./tests/network/haproxyprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-singbox-probe ./tests/network/singboxprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-routing-probe ./tests/network/routingprobe

FROM debian:bookworm-slim

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        coreutils \
        haproxy \
        iproute2 \
        iptables \
        nftables \
        procps \
        socat \
        tcpdump \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /lab
COPY tests/network/lab.sh tests/network/run.sh ./
COPY --from=builder /out/egress-inventory-probe /usr/local/bin/egress-inventory-probe
COPY --from=builder /out/egress-nat-probe /usr/local/bin/egress-nat-probe
COPY --from=builder /out/egress-counter-probe /usr/local/bin/egress-counter-probe
COPY --from=builder /out/egress-iptables-probe /usr/local/bin/egress-iptables-probe
COPY --from=builder /out/egress-haproxy-probe /usr/local/bin/egress-haproxy-probe
COPY --from=builder /out/egress-singbox-probe /usr/local/bin/egress-singbox-probe
COPY --from=builder /out/egress-routing-probe /usr/local/bin/egress-routing-probe
COPY --from=singbox /usr/local/bin/sing-box /usr/local/bin/sing-box
COPY internal/singbox/testdata /lab/singbox-fixtures
RUN chmod 0755 ./lab.sh ./run.sh /usr/local/bin/egress-inventory-probe /usr/local/bin/egress-nat-probe /usr/local/bin/egress-counter-probe /usr/local/bin/egress-iptables-probe /usr/local/bin/egress-haproxy-probe /usr/local/bin/egress-singbox-probe /usr/local/bin/egress-routing-probe /usr/local/bin/sing-box

ENTRYPOINT ["/lab/run.sh"]
