# Project automation for 'a' CLI wrapper for age encryption
# Set the shell to bash for compatibility

set shell := ["bash", "-cu"]

# Variables

BINARY := "a"

# Default: show help
default:
    @just --list

# Format all code (Go, YAML, Markdown)
format:
    gofmt -s -w .
    goimports -w .
    yamlfmt -conf .yamlfmt.yml .
    markdownlint -c .markdownlint.json --fix '**/*.md'

# Lint Go code and configs
lint:
    golangci-lint run
    yamllint -c .yamllint.yml .
    markdownlint -c .markdownlint.json '**/*.md'

# Run all tests
test:
    go test -v ./...

# Run tests with coverage; writes coverage.out and opens the HTML report
coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1
    go tool cover -html=coverage.out -o coverage.html

# Build the binary
build:
    go build -o {{ BINARY }} .

# Run GoReleaser (dry-run by default)
release:
    goreleaser release --clean --skip-publish --snapshot

# Run GoReleaser for actual release (requires env vars)
release-publish:
    goreleaser release --clean

# Run prek or pre-commit, prefer prek hooks on all files
precommit:
    @command -v prek >/dev/null 2>&1 && prek run --all-files || pre-commit run --all-files

# Update Go modules
tidy:
    go mod tidy

# Clean build artifacts
clean:
    rm -rf {{ BINARY }} dist/ coverage* *.log

# Show help
help:
    @echo "Available commands:"
    @just --list
