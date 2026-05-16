BIN    := trecd
GO     := go
LDFLAGS:=

build: $(BIN)

$(BIN): cmd/trecd/main.go
	$(GO) build -o $@ $(LDFLAGS) ./cmd/trecd

install:
	$(GO) install $(LDFLAGS) ./cmd/trecd

clean:
	rm -f $(BIN)

test:
	$(GO) test -count=1 ./...

bench:
	$(GO) test ./... -bench=. -benchmem -benchtime=1000ms

.PHONY: build install clean test bench
