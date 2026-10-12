# Ocelot BitTorrent Tracker — Go edition
# Usage:
#   make              build local binary
#   make linux        cross-compile for IONOS VPS (linux/amd64)
#   make test         run full test suite with -race
#   make bench        run benchmarks
#   make vet          run go vet + staticcheck (if installed)
#   make clean        remove build artefacts
#   make deploy HOST=<vps-ip>   scp binary and restart service

BINARY      := ocelot
CMD         := ./cmd/ocelot
GOFLAGS     := -trimpath
LDFLAGS     := -s -w
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     += -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

# Remote deploy defaults
REMOTE_USER := root
REMOTE_PATH := /usr/local/bin/ocelot
SERVICE     := ocelot

.PHONY: all linux linux-arm test bench vet clean deploy

# ── Build ─────────────────────────────────────────────────────────────────────

all: $(BINARY)

$(BINARY):
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)
	@echo "Built: $(BINARY)"

linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 $(CMD)
	@echo "Cross-compiled: $(BINARY)-linux-amd64"

linux-arm:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-arm64 $(CMD)
	@echo "Cross-compiled: $(BINARY)-linux-arm64"

# ── Quality ───────────────────────────────────────────────────────────────────

test:
	go test ./... -race -timeout 120s

bench:
	go test ./tracker/... -bench=. -benchmem -run='^$$' -timeout 120s

vet:
	go vet ./...
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || true

# ── Deploy ────────────────────────────────────────────────────────────────────

deploy: linux
ifndef HOST
	$(error HOST is not set.  Usage: make deploy HOST=<vps-ip>)
endif
	scp $(BINARY)-linux-amd64 $(REMOTE_USER)@$(HOST):$(REMOTE_PATH)
	ssh $(REMOTE_USER)@$(HOST) "systemctl restart $(SERVICE) && systemctl status $(SERVICE) --no-pager"
	@echo "Deployed $(VERSION) to $(HOST)"

# Send SIGHUP to reload config without restart
reload:
ifndef HOST
	$(error HOST is not set.  Usage: make reload HOST=<vps-ip>)
endif
	ssh $(REMOTE_USER)@$(HOST) "systemctl kill -s HUP $(SERVICE)"

# Send SIGUSR1 to reload torrent/user/whitelist state
reload-state:
ifndef HOST
	$(error HOST is not set.  Usage: make reload-state HOST=<vps-ip>)
endif
	ssh $(REMOTE_USER)@$(HOST) "systemctl kill -s USR1 $(SERVICE)"

# ── Housekeeping ──────────────────────────────────────────────────────────────

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64
