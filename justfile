set shell := ["bash", "-euo", "pipefail", "-c"]

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
commit  := `git rev-parse --verify -q HEAD || echo unknown`
date    := `date -u +%Y-%m-%dT%H:%M:%SZ`
pkg     := "github.com/temikus/meles/internal/buildinfo"
ldflags := "-s -w -X " + pkg + ".Version=" + version + " -X " + pkg + ".Commit=" + commit + " -X " + pkg + ".Date=" + date

default:
    @just --list

# Build the meles binary into bin/
build:
    go build -trimpath -ldflags '{{ldflags}}' -o bin/meles ./cmd/meles

# Run unit tests with the race detector and coverage
test:
    go test -race -coverprofile=coverage.out ./...

# Run integration tests against a real repo (token from GH_TOKEN or gh auth token).
# -p 1: the tests share one repo and one rate limit, so they must not overlap.
test-integration repo=env("MELES_IT_REPO", "Temikus/meles-sandbox"):
    MELES_INTEGRATION=1 MELES_IT_REPO={{repo}} go test -race -count=1 -p 1 -run Integration ./...

# Lint with golangci-lint
lint:
    golangci-lint run

# Format code
fmt:
    golangci-lint fmt

# Run meles with arguments, e.g. `just run plan`
run *ARGS:
    go run -ldflags '{{ldflags}}' ./cmd/meles {{ARGS}}

# Show per-function coverage
cover: test
    go tool cover -func=coverage.out
