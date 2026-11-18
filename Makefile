.PHONY: build check fmt test test-race vet

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

test-race:
	go test -race ./...

check: fmt vet test-race build
	git diff --check
