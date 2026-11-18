#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS="${GOFLAGS:-} -mod=vendor"
unformatted=$(gofmt -l . | grep -vE '^(testdata|vendor)/' || true)
if [ -n "$unformatted" ]; then echo "gofmt:"; echo "$unformatted"; exit 1; fi
go vet ./...
go test -count=1 ./...
go run github.com/kapetan-io/tiger/cmd/tiger check ./...
