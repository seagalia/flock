FROM envoyproxy/envoy:v1.40.0
LABEL flock-e2e=true
RUN apt-get update \
    && apt-get install -y --no-install-recommends nftables ipvsadm iproute2 curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
