VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
REPO    ?= joshlopes/minimalist-cost-tracker
LDFLAGS := -X main.version=$(VERSION) -X github.com/lendable/minimalist-cost-tracker/internal/selfupdate.DefaultRepo=$(REPO)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test fmt vet dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/cost-tracker ./cmd/cost-tracker

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

# dist cross-compiles the release tarballs + checksums into ./dist, matching
# what .github/workflows/release.yml produces (CGO is off: pure-Go sqlite).
dist: clean
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags "-s -w $(LDFLAGS)" -o dist/cost-tracker ./cmd/cost-tracker; \
		tar -czf dist/cost-tracker_$${os}_$${arch}.tar.gz -C dist cost-tracker; \
		rm dist/cost-tracker; \
	done
	@cd dist && (command -v sha256sum >/dev/null 2>&1 && sha256sum cost-tracker_*.tar.gz > SHA256SUMS \
		|| shasum -a 256 cost-tracker_*.tar.gz > SHA256SUMS)
	@echo "dist/ ready:" && ls -1 dist

clean:
	rm -rf dist
