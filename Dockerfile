# Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build binary. VERSION is overridden by the release / pr-images workflows
# with the git tag (or pr-N-<sha> identifier) so the running binary reports
# its own version at startup.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o gatekeeperd ./cmd/gatekeeperd

# Runtime stage
FROM alpine:3.24.1

LABEL org.opencontainers.image.source=https://github.com/Tight-Line/gatekeeper

# Pick up patched packages from the v3.24 repo; the base image tag lags
# behind apk for openssl and similar security updates.
RUN apk --no-cache upgrade && \
    apk --no-cache add ca-certificates libcap

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/gatekeeperd .

# Allow binding to privileged ports and create non-root user
RUN setcap cap_net_bind_service=+ep ./gatekeeperd && \
    addgroup -g 1000 gatekeeper && \
    adduser -u 1000 -G gatekeeper -s /bin/sh -D gatekeeper && \
    mkdir -p /etc/gatekeeper /var/cache/gatekeeper && \
    chown -R gatekeeper:gatekeeper /app /etc/gatekeeper /var/cache/gatekeeper

USER gatekeeper

# Expose ports
EXPOSE 80 443 8080 9090

# Run the proxy
ENTRYPOINT ["./gatekeeperd"]
CMD ["-config", "/etc/gatekeeper/config.yaml"]
