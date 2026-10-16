GO ?= go
GOENV = env GOMAXPROCS=1 GOMEMLIMIT=64MiB GOGC=5 GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod GOFLAGS=-p=1

.PHONY: prepare format format-check test vet build check release clean

prepare:
	mkdir -p .cache/go-build .cache/go-mod build

format:
	gofmt -w $$(find cmd internal -type f -name '*.go' -print)

format-check:
	@test -z "$$(gofmt -l $$(find cmd internal -type f -name '*.go' -print))" || \
		{ echo "Go files need formatting; run make format"; exit 1; }

test: prepare
	$(GOENV) $(GO) test -buildvcs=false -p=1 ./...

vet: prepare
	$(GOENV) $(GO) vet ./...

build: prepare
	$(GOENV) $(GO) build -buildvcs=false -p=1 -o build/lifepowerctl ./cmd/lifepowerctl

# Run sequentially: this target must never start concurrent Go processes on the Pi.
check: format-check test vet build

release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=vMAJOR.MINOR.PATCH[-PRERELEASE]"; exit 2; }
	./scripts/build-release.sh "$(VERSION)"

clean:
	rm -f build/lifepowerctl coverage.out
