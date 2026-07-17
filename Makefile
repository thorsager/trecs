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

DOCKER_COMPOSE := docker compose -f docker/compose.yml

docker-build:
	$(DOCKER_COMPOSE) build

docker-up:
	$(DOCKER_COMPOSE) up

docker-up-d:
	$(DOCKER_COMPOSE) up -d

docker-stop:
	$(DOCKER_COMPOSE) stop

docker-down:
	$(DOCKER_COMPOSE) down

docker-logs:
	$(DOCKER_COMPOSE) logs -f

.PHONY: build install clean test integrationtest bench race lint docker-build docker-up docker-up-d docker-stop docker-down docker-logs
