# /makefile
# Builds, tests and packages Markspace. Run make help for the targets.

BINARY := markspace
GOFLAGS ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS := -X main.buildVersion=$(VERSION)

.PHONY: help build run vet fmt check test race fuzz client python layout flows fixtures images clean

help:
	@echo "build     build the markspace binary"
	@echo "run       build and serve with markspace.yaml"
	@echo "check     gofmt, go vet and make test"
	@echo "test      Go tests, JavaScript client tests and Python tests"
	@echo "race      Go tests with the race detector"
	@echo "fuzz      every Go fuzz target for FUZZTIME (default 30s)"
	@echo "client    JavaScript client tests in test/client, run in Node"
	@echo "python    bookmark wiki plugin and Python SDK tests"
	@echo "layout    layout audit of the running app in Chromium"
	@echo "flows     scripted click-through flows in Chromium"
	@echo "fixtures  layout audit checks against pages with planted bugs"
	@echo "images    build the server and wiki plugin images with podman"

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/markspace

run: build
	./$(BINARY) serve -config markspace.yaml

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd internal sdk test tools

check: fmt vet test

test:
	go test ./...
	$(MAKE) client python

race:
	go test -race ./...

# fuzz runs every fuzz target for FUZZTIME each. Seed inputs also run in
# plain go test.
FUZZTIME ?= 30s
fuzz:
	@for pkg in $$(go list ./...); do \
		for target in $$(go test -list '^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz'); do \
			echo "== $$pkg $$target"; \
			go test -run '^$$' -fuzz "^$$target$$" -fuzztime $(FUZZTIME) $$pkg || exit 1; \
		done; \
	done

client:
	NODE_OPTIONS=--max-old-space-size=1024 node --test --test-concurrency=2 'test/client/**/*.test.mjs'

python:
	python3 -m unittest discover -s plugins/wiki/test
	python3 -m unittest discover -s sdk/python/test

# layout builds the server into a temporary directory, starts it on a free
# port with fresh data, seeds it and audits the layout of every page state in
# Chromium. It exits non-zero on any finding. LAYOUT_ARGS passes options to
# layout.mjs, such as --states=rail --profiles=1440. See test/browser/readme.md.
LAYOUT_ARGS ?=
# layout.mjs reads LAYOUT_ARGS from the environment, so the shell never parses
# the regexes in it. Options are separated by spaces.
layout: export LAYOUT_ARGS := $(LAYOUT_ARGS)
layout:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/markspace-layout.XXXXXX") && \
	echo "layout: temporary directory $$tmp" && \
	CGO_ENABLED=0 go build $(GOFLAGS) -o "$$tmp/markspace" ./cmd/markspace && \
	node test/browser/layout.mjs --bin="$$tmp/markspace" --tmp="$$tmp"

# flows builds the server the same way and clicks through the scripted flows
# in flows.mjs and history.mjs.
flows:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/markspace-flows.XXXXXX") && \
	echo "flows: temporary directory $$tmp" && \
	CGO_ENABLED=0 go build $(GOFLAGS) -o "$$tmp/markspace" ./cmd/markspace && \
	node test/browser/flows.mjs --bin="$$tmp/markspace" --tmp="$$tmp/flows" && \
	node test/browser/history.mjs --bin="$$tmp/markspace" --tmp="$$tmp/history"

# fixtures runs the layout audit on the pages in test/browser/fixtures, each
# with planted bugs, and fails unless each page produces exactly its expected
# findings.
fixtures:
	node test/browser/fixtures.mjs

images:
	podman build -f containerfile --build-arg VERSION=$(VERSION) -t markspace .
	podman build -f plugins/wiki/containerfile -t markspace-wiki .

clean:
	rm -f $(BINARY)
