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

# Two profiles merged, not one: main() only ever runs in the subprocess
# TestCLIIntegration launches, so `go test -coverprofile` alone reports it at 0%
# no matter how thoroughly the test drives it. -test.gocoverdir collects the unit
# runs in the same binary format, A_INTEGRATION_GOCOVERDIR makes the integration
# test build its binary with -cover, and covdata merges the two.
[doc("Run tests with coverage; writes coverage.out and opens the HTML report")]
coverage:
    rm -rf .coverdata && mkdir -p .coverdata/unit .coverdata/integration .coverdata/merged
    A_INTEGRATION_GOCOVERDIR="$PWD/.coverdata/integration" \
        go test -cover ./... -args -test.gocoverdir="$PWD/.coverdata/unit"
    go tool covdata merge -i=.coverdata/unit,.coverdata/integration -o=.coverdata/merged
    go tool covdata textfmt -i=.coverdata/merged -o=coverage.out
    go tool cover -func=coverage.out | tail -1
    go tool cover -html=coverage.out -o coverage.html

# Build the binary
build:
    go build -o {{ BINARY }} .

# No --skip-publish: GoReleaser v2 removed the individual --skip-* flags in
# favour of --skip=<option>, so the pinned version rejected the whole command
# with "unknown flag" and this rehearsal never ran. --snapshot already implies
# --skip=announce,publish,validate, so nothing needs to replace it.
#
# [doc] rather than a plain comment: `just --list` shows only the line adjacent
# to the recipe, so a multi-line rationale would become the description.
[doc("Run GoReleaser (dry-run by default)")]
release:
    goreleaser release --clean --snapshot

# Run GoReleaser for actual release (requires env vars)
release-publish:
    goreleaser release --clean

# An if, not `probe && prek || pre-commit`: `||` fires on any false left-hand
# side, so a prek run that *found something* fell through and ran the entire
# hook suite again under pre-commit, reporting that second tool's verdict --
# or "pre-commit: command not found", since mise pins prek precisely so the
# fallback is never needed.
[doc("Run prek or pre-commit, prefer prek hooks on all files")]
precommit:
    @if command -v prek >/dev/null 2>&1; then \
        prek run --all-files; \
    else \
        pre-commit run --all-files; \
    fi

# golangci-lint's gosec audits this repo's source; nothing audits the modules
# it links. Same command test.yml runs, so a local pass means a CI pass.
[doc("Check the dependency graph for known vulnerabilities")]
vuln:
    govulncheck ./...

# Statement coverage cannot see these: `a && b` counts as one block however each
# term went. The script wraps gobco because gobco exits 0 even when it finds
# gaps. Same command test.yml runs.
[doc("Check every condition is evaluated both true and false")]
conditions:
    ./scripts/check-conditions.sh

# Branches rather than conditions: a coarser view, useful when hunting an
# unreached arm rather than an unexercised term. Not gated in CI -- conditions
# subsume it.
[doc("Report branch coverage (not gated)")]
branches:
    gobco -branch .
    gobco -branch ./internal/cmd

# Regenerate THIRD_PARTY_NOTICES.md (required by the BSD-3/Apache-2.0 deps)
notices:
    ./scripts/gen-notices.sh

# Update Go modules
tidy:
    go mod tidy

# Clean build artifacts
clean:
    rm -rf {{ BINARY }} dist/ coverage* .coverdata *.log
