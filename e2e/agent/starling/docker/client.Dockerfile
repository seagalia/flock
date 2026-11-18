FROM alpine:3.24
LABEL flock-e2e=true
RUN apk add --no-cache curl socat iproute2 openssl iputils-arping
CMD ["sleep", "infinity"]
