.PHONY: build build-relay build-all test clean run lint lint-fix docker setup-hooks

# Build variables
VERSION?=0.1.0
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# Default target
all: lint test build-all

# Build the server binary
build:
	go build $(LDFLAGS) -o bin/gatekeeperd ./cmd/gatekeeperd

# Build the relay client binary
build-relay:
	go build $(LDFLAGS) -o bin/gatekeeper-relay ./cmd/gatekeeper-relay

# Build all binaries
build-all: build build-relay

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out -tags=ci ./...
	go tool cover -html=coverage.out -o coverage.html

# Run the server locally (for development)
run: build
	./bin/gatekeeperd -config config/example.yaml -listen :8080

# Run the relay client locally (for development)
run-relay: build-relay
	./bin/gatekeeper-relay -config config/relay-client-example.yaml

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Run linter
lint:
	golangci-lint run ./...

# Run linter and fix issues automatically where possible
lint-fix:
	golangci-lint run --fix ./...

# Build Docker images
docker:
	docker build -t gatekeeper:$(VERSION) -f Dockerfile .
	docker build -t gatekeeper-relay:$(VERSION) -f Dockerfile.relay .

# Format code
fmt:
	go fmt ./...
	goimports -w -local github.com/tight-line/gatekeeper .

# Tidy dependencies
tidy:
	go mod tidy

# Install development tools
tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest

# Set up git hooks for development
setup-hooks:
	@echo "Installing pre-commit hook..."
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

# Verify everything (used by CI and before committing)
check: lint test
	@echo "All checks passed."
