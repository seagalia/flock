FROM alpine:3.24
LABEL flock-e2e=true
RUN apk add --no-cache ca-certificates
# built on the host by e2e/images, with a stub dashboard
COPY e2e/.build/flock /usr/local/bin/flock
ENTRYPOINT ["/usr/local/bin/flock", "controller", "serve", "-c", "/etc/flock/controller.yml"]
