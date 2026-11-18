# starling-waf on its own, for envoys the starling agent doesn't run (service mode of the agent's WAF config).
# Built with the tag re2_cgo, the @rx operator of coraza runs on libre2 through cgo, much faster than Go's regexp on the
# OWASP CRS (see agent/starling/waf/wafproccessor/re2_cgo.go). Built by make docker-waf.

# trixie on both stages, the binary links the libre2 of the build stage
FROM golang:1.27-trixie AS build
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN apt-get update \
    && apt-get install -y --no-install-recommends libre2-dev pkg-config g++ \
    && rm -rf /var/lib/apt/lists/*
ENV CGO_ENABLED=1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -tags re2_cgo \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/starling-waf ./agent/starling/waf/starling-waf

FROM debian:trixie-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends libre2-11 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --home-dir /var/run/starling-waf starling-waf
COPY --from=build /out/starling-waf /usr/local/bin/starling-waf
USER starling-waf
WORKDIR /var/run/starling-waf
VOLUME /var/run/starling-waf
ENTRYPOINT ["starling-waf", "-s", "/var/run/starling-waf/waf.sock", "-m", "/var/run/starling-waf/waf-metrics.sock"]
