BINARY := dtwiz
GO     := go

.PHONY: build install test lint clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	$(GO) build -ldflags "-X github.com/dietermayrhofer/dtwiz/cmd.Version=$(VERSION)" -o $(BINARY) .

install:
	$(GO) install .

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)
