# The flock image: the single flock binary (controller, agent and cli) on top of envoy, so the starling agent can run
# in a container and flock dev works out of the box. The slim target is the same binary with no envoy, for the
# controller, the provisioner and the F5 agent.

FROM node:24 AS webui
WORKDIR /src
COPY Makefile ./
COPY webui ./webui
RUN make webui

FROM golang:1.27 AS build
ARG VERSION=0.0.0-dev
ARG COMMIT=none
ARG DATE=unknown
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=webui /src/webui/dist ./webui/dist
RUN go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /out/flock .

# slim sits before the full stage, so a build with no --target gives the full image
FROM gcr.io/distroless/static-debian12:nonroot AS slim
COPY --from=build /out/flock /usr/local/bin/flock
ENTRYPOINT ["/usr/local/bin/flock"]

FROM envoyproxy/envoy:v1.40.0
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates nftables ipvsadm iproute2 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/flock /usr/local/bin/flock
VOLUME /var/lib/flock
EXPOSE 8443
ENTRYPOINT ["flock"]
CMD ["dev", "--data-dir", "/var/lib/flock", "--listen-host", "0.0.0.0", "--envoy-path", "/usr/local/bin/envoy", "--no-browser"]
