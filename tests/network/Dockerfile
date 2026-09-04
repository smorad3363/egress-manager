FROM golang:1.27.1 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY tests/network/inventoryprobe ./tests/network/inventoryprobe
COPY tests/network/natprobe ./tests/network/natprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-inventory-probe ./tests/network/inventoryprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-nat-probe ./tests/network/natprobe

FROM debian:bookworm-slim

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        coreutils \
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
RUN chmod 0755 ./lab.sh ./run.sh /usr/local/bin/egress-inventory-probe /usr/local/bin/egress-nat-probe

ENTRYPOINT ["/lab/run.sh"]
