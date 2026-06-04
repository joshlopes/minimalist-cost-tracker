VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test fmt vet

build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/cost-tracker ./cmd/cost-tracker

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...
