# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o gatekeeperd ./cmd/gatekeeperd

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/gatekeeperd .

# Create config directory
RUN mkdir -p /etc/gatekeeper

# Expose ports
EXPOSE 80 443 8080 9090

# Run the proxy
ENTRYPOINT ["./gatekeeperd"]
CMD ["-config", "/etc/gatekeeper/config.yaml"]
