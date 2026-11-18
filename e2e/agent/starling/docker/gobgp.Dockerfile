FROM golang:1.27 AS build
RUN CGO_ENABLED=0 go install github.com/osrg/gobgp/v4/cmd/gobgpd@v4.10.0 \
    && CGO_ENABLED=0 go install github.com/osrg/gobgp/v4/cmd/gobgp@v4.10.0

FROM debian:trixie-slim
LABEL flock-e2e=true
COPY --from=build /go/bin/gobgpd /go/bin/gobgp /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/gobgpd", "-f", "/etc/gobgpd.toml"]
