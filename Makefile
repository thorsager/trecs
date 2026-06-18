BIN    := trecsd
GO     := go
LDFLAGS:=

PACKAGES := $(shell go list ./... | grep -v integrationtest)

build: $(BIN)

$(BIN): cmd/trecsd/main.go
	$(GO) build -o $@ $(LDFLAGS) ./cmd/trecsd

install:
	$(GO) install $(LDFLAGS) ./cmd/trecsd

clean:
	rm -f $(BIN)

test:
	$(GO) test -count=1  $(PACKAGES)

integrationtest:
	$(GO) test -count=1 ./integrationtest/...

bench:
	$(GO) test ./... -bench=. -benchmem -benchtime=1000ms

race:
	$(GO) test -race -count=1 $(PACKAGES)

lint:
	golangci-lint run

.PHONY: build install clean test integrationtest bench race lint
