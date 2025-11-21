.PHONY: build test lint clean

build:
	CGO_LDFLAGS="-Wl,-rpath,$(PWD)/llama-go" go build -o ccrider ./cmd/ccrider

test:
	go test ./... -v -race -coverprofile=coverage.out

lint:
	golangci-lint run ./...

clean:
	rm -f ccrider coverage.out

install:
	go install ./cmd/ccrider
