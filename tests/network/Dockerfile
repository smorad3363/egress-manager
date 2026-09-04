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
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-inventory-probe ./tests/network/inventoryprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-nat-probe ./tests/network/natprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-counter-probe ./tests/network/counterprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-iptables-probe ./tests/network/iptablesprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-haproxy-probe ./tests/network/haproxyprobe

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
    && rm -rf /var/lib/apt/lists/*

WORKDIR /lab
COPY tests/network/lab.sh tests/network/run.sh ./
COPY --from=builder /out/egress-inventory-probe /usr/local/bin/egress-inventory-probe
COPY --from=builder /out/egress-nat-probe /usr/local/bin/egress-nat-probe
COPY --from=builder /out/egress-counter-probe /usr/local/bin/egress-counter-probe
COPY --from=builder /out/egress-iptables-probe /usr/local/bin/egress-iptables-probe
COPY --from=builder /out/egress-haproxy-probe /usr/local/bin/egress-haproxy-probe
RUN chmod 0755 ./lab.sh ./run.sh /usr/local/bin/egress-inventory-probe /usr/local/bin/egress-nat-probe /usr/local/bin/egress-counter-probe /usr/local/bin/egress-iptables-probe /usr/local/bin/egress-haproxy-probe

ENTRYPOINT ["/lab/run.sh"]
