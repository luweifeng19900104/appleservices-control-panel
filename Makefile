# appleservices — one Go module, two platform distributions.
#   make test            go vet + go test (host OS)
#   make linux           dist/linux + dist/appleservices-linux-dist.tar.gz
#   make windows         dist/windows + dist/appleservices-windows-dist.zip (cross-built, CGO off)
#   make dist            both
#   make src             dist/appleservices-repo-src.zip (git archive of HEAD)
#   make clean
GO      ?= go
GOARCH  ?= amd64
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || date -u +%Y%m%d%H%M%S)

.PHONY: test vet linux windows dist src clean

vet:
	$(GO) vet ./...

test: vet
	$(GO) test ./...

linux:
	GO=$(GO) GOARCH=$(GOARCH) VERSION=$(VERSION) sh scripts/build-linux.sh

windows:
	GO=$(GO) VERSION=$(VERSION) sh scripts/build-windows-cross.sh

dist: linux windows

src:
	mkdir -p dist
	git archive --format=zip --prefix=appleservices/ -o dist/appleservices-repo-src.zip HEAD

clean:
	rm -rf dist
