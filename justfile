# Run tests with the race detector
default:
    @just --list

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

# Build the shelf binary
build:
    @echo "Building shelf version: {{version}}"
    CGO_ENABLED=0 go build -ldflags "-s -w -X main.version={{version}}" -o shelf ./cmd/shelf

# Run all tests
test:
    go test ./...

# Run tests with the race detector
test-race:
    go test -race ./...

# Run go vet
vet:
    go vet ./...

# Format code
fmt:
    golangci-lint fmt

# Lint: golangci-lint (uses built-in defaults)
lint:
    golangci-lint run

# Print the completion script for a shell (bash, zsh, fish, powershell)
completions shell="bash":
    ./shelf completion {{shell}}

# Clean build artifacts
clean:
    rm -f shelf
