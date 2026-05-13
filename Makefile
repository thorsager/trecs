test:
	go test -count=1 ./...

bench:
	go test ./... -bench=. -benchmem -benchtime=1000ms

.PHONY: test bench
