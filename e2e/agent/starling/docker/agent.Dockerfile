FROM envoyproxy/envoy:v1.40.0
LABEL flock-e2e=true
RUN apt-get update \
    && apt-get install -y --no-install-recommends nftables ipvsadm iproute2 kmod curl procps ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# built on the host by e2e/images
COPY e2e/.build/flock /usr/local/bin/flock
COPY e2e/agent/starling/docker/run-agent.sh /usr/local/bin/run-agent.sh
ENTRYPOINT ["/usr/local/bin/run-agent.sh"]
