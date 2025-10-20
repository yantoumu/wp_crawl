.PHONY: build run test clean install deps fmt lint help

# Variables
BINARY_NAME=wp_crawl
GO=go
GOFLAGS=-v
MAIN_PATH=cmd/scanner/main.go
VERSION=$(shell git describe --tags --always --dirty)
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Default target
all: build

## help: Show this help message
help:
	@echo "Available targets:"
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

## build: Build the binary
build:
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME) $(MAIN_PATH)

## run: Run the scanner with default config
run: build
	./$(BINARY_NAME) scan --crawl CC-MAIN-2025-38

## test: Run all tests
test:
	$(GO) test -v -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

## bench: Run benchmarks
bench:
	$(GO) test -bench=. -benchmem ./...

## deps: Download dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

## fmt: Format code
fmt:
	$(GO) fmt ./...
	gofmt -s -w .

## lint: Run linters
lint:
	@if ! which golangci-lint > /dev/null; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	golangci-lint run

## vet: Run go vet
vet:
	$(GO) vet ./...

## clean: Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html
	rm -f *.log *.json *.gz
	rm -rf dist/

## install: Install the binary
install: build
	$(GO) install $(LDFLAGS) $(MAIN_PATH)

## docker-build: Build Docker image
docker-build:
	docker build -t wp_crawl:$(VERSION) .

## docker-run: Run in Docker
docker-run:
	docker run --rm -v $(PWD):/data wp_crawl:$(VERSION) scan --crawl CC-MAIN-2025-38

## release: Create release build for multiple platforms
release:
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "Release builds created in dist/"

## profile: Run with profiling enabled
profile: build
	./$(BINARY_NAME) scan --crawl CC-MAIN-2025-38 --config configs/profile.yaml &
	@echo "Profiling server started at http://localhost:6060/debug/pprof/"
	@echo "Press Ctrl+C to stop"

## dev: Run in development mode with live reload
dev:
	@if ! which air > /dev/null; then \
		echo "Installing air..."; \
		go install github.com/cosmtrek/air@latest; \
	fi
	air -c .air.toml