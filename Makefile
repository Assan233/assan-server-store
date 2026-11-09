.PHONY: run build fmt vet test check

run:
	go run ./cmd/server

build:
	go build -trimpath -o bin/server ./cmd/server

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

test:
	go test -race ./...

check: vet test
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
