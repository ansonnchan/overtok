.PHONY: build check fmt release test test-postgres test-race vet

# Keep build artifacts out of user-global caches so clean-checkout verification
# behaves consistently in local sandboxes and CI.
GOCACHE ?= /tmp/tokenholic-go-cache
GOPATH ?= /tmp/tokenholic-gopath
export GOCACHE
export GOPATH

build:
	go build ./...

release:
	test -n "$(VERSION)"
	go run ./cmd/release -version "$(VERSION)" -output dist

fmt:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test ./...

test-postgres:
	test -n "$$TOKENHOLIC_TEST_DATABASE_URL"
	go test ./server/postgres -run Postgres -count=1

test-race:
	go test -race ./...

check: fmt vet test-race build
	git diff --check
