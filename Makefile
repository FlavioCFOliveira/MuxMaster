.PHONY: test test-race lint vet cover bench api help check clean

help:
	@echo "MuxMaster — make targets"
	@echo "  test       — run tests (excluding /reports/)"
	@echo "  test-race  — run tests with -race"
	@echo "  vet        — run go vet"
	@echo "  lint       — run golangci-lint and staticcheck"
	@echo "  cover      — run tests with coverage profile"
	@echo "  bench      — run all benchmarks (excluding /reports/, /competitor/)"
	@echo "  api        — regenerate api.md from go doc"
	@echo "  check      — vet + staticcheck + lint + race"
	@echo "  clean      — go clean -testcache"

test:
	go test -count=1 $$(go list ./... | grep -v '/reports/')

test-race:
	go test -race -count=1 $$(go list ./... | grep -v '/reports/')

vet:
	go vet $$(go list ./... | grep -v '/reports/')

lint:
	golangci-lint run
	staticcheck $$(go list ./... | grep -v '/reports/')

cover:
	go test -coverprofile=coverage.out -covermode=atomic $$(go list ./... | grep -v '/reports/')
	go tool cover -func=coverage.out | tail -1

bench:
	go test -bench=. -benchmem -run=^$$ $$(go list ./... | grep -v '/reports/' | grep -v '/competitor/')

api:
	@{ \
	  echo "# Public API"; \
	  echo ""; \
	  echo "Auto-generated from \`go doc\`. Do not edit by hand —"; \
	  echo "regenerate with: \`make api\`."; \
	  echo ""; \
	  echo "See [COMPATIBILITY.md](./COMPATIBILITY.md) for the SemVer tier policy."; \
	  echo ""; \
	  echo "## github.com/FlavioCFOliveira/MuxMaster"; \
	  echo ""; \
	  echo '```go'; \
	  go doc -all .; \
	  echo '```'; \
	  echo ""; \
	  echo "## github.com/FlavioCFOliveira/MuxMaster/middleware"; \
	  echo ""; \
	  echo '```go'; \
	  go doc -all ./middleware; \
	  echo '```'; \
	} > api.md
	@echo "wrote api.md ($$(wc -l < api.md) lines)"

check: vet lint test-race

clean:
	go clean -testcache
