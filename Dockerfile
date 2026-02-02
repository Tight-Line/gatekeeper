# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o gatekeeperd ./cmd/gatekeeperd

# Runtime stage
FROM alpine:3.23.3

LABEL org.opencontainers.image.source=https://github.com/Tight-Line/gatekeeper

RUN apk --no-cache add ca-certificates libcap

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
