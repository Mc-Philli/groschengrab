.PHONY: run build test tidy

run:
	go run ./cmd/server

build:
	go build -o bin/household-app ./cmd/server

test:
	go vet ./...
	go test ./...

tidy:
	go mod tidy
