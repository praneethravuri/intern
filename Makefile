.PHONY: build test lint clean

build:
	go build -ldflags "-X main.version=$$(git describe --tags --always --dirty)" -o tether ./cmd/tether

test:
	go test -race ./...

lint:
	golangci-lint run

clean:
	rm -f tether
