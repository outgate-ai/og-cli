BINARY_NAME = og
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
LDFLAGS = -s -w -X github.com/outgate-ai/og-cli/version.Version=$(VERSION)
BUILD_DIR = dist

.PHONY: build clean test all platforms

# Default: build for current platform
build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) .

# Build for all platforms
platforms: \
	$(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 \
	$(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 \
	$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 \
	$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64

$(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $@ .

$(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $@ .

$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $@ .

$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $@ .

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Install locally
install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
