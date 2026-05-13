test:
	go test ./...

bench:
	go test ./... -bench=. -benchmem -benchtime=1000ms

.PHONY: test bench
