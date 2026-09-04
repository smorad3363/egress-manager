FROM debian:bookworm-slim

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        coreutils \
        iproute2 \
        nftables \
        procps \
        socat \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /lab
COPY lab.sh run.sh ./
RUN chmod 0755 ./lab.sh ./run.sh

ENTRYPOINT ["/lab/run.sh"]
