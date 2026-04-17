.PHONY: test race bench vet lint staticcheck check clean

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -bench=. -benchmem ./...

vet:
	go vet ./...

lint:
	golangci-lint run

staticcheck:
	staticcheck ./...

check: vet staticcheck lint race

clean:
	go clean -testcache
