FROM golang:1.27.1 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY tests/network/inventoryprobe ./tests/network/inventoryprobe
RUN CGO_ENABLED=0 go build -trimpath -o /out/egress-inventory-probe ./tests/network/inventoryprobe

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
RUN chmod 0755 ./lab.sh ./run.sh /usr/local/bin/egress-inventory-probe

ENTRYPOINT ["/lab/run.sh"]
