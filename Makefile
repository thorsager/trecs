BIN    := trecsd
GO     := go
LDFLAGS:=

build: $(BIN)

$(BIN): cmd/trecsd/main.go
	$(GO) build -o $@ $(LDFLAGS) ./cmd/trecsd

install:
	$(GO) install $(LDFLAGS) ./cmd/trecsd

clean:
	rm -f $(BIN)

test:
	$(GO) test -count=1 -skip=TestIntegration ./...

integrationtest:
	$(GO) test -count=1 ./integrationtest/

bench:
	$(GO) test ./... -bench=. -benchmem -benchtime=1000ms

.PHONY: build install clean test integrationtest bench
