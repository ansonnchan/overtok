.PHONY: build check fmt test test-postgres test-race vet

# Keep build artifacts out of user-global caches so clean-checkout verification
# behaves consistently in local sandboxes and CI.
GOCACHE ?= /tmp/overtok-go-cache
GOPATH ?= /tmp/overtok-gopath
export GOCACHE
export GOPATH

build:
	go build ./...

fmt:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test ./...

test-postgres:
	test -n "$$OVERTOK_TEST_DATABASE_URL"
	go test ./server/postgres -run Postgres -count=1

test-race:
	go test -race ./...

check: fmt vet test-race build
	git diff --check
