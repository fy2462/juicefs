FROM ubuntu:22.04

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
      ca-certificates \
      curl \
      fuse3 \
      procps \
      redis-tools \
      util-linux && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /work /mnt/jfs /cache

WORKDIR /work
