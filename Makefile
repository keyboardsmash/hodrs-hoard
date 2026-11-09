# Convenience targets. Plain `go build ./cmd/hoard` works too.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build run test vet clean

# CGO_ENABLED=0 gives a fully static binary (see CLAUDE.md: no cgo).
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o hoard ./cmd/hoard

run:
	go run ./cmd/hoard

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f hoard
