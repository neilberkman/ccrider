.PHONY: build test lint clean

build:
	go build -o ccrider cmd/ccrider/main.go

test:
	go test ./... -v -race -coverprofile=coverage.out

lint:
	golangci-lint run ./...

clean:
	rm -f ccrider coverage.out

install:
	go install ./cmd/ccrider
