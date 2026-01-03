# Media library Makefile
# Builds native codec libraries and places them in ./build/

.PHONY: all build deps clean test help run-example

# Get the absolute path to the build directory
BUILD_DIR := $(shell pwd)/build

# Default target
all: build

# Build all native libraries
build:
	$(MAKE) -C clib all

# Build with system libraries (faster, for development)
build-system:
	$(MAKE) -C clib all USE_SYSTEM_LIBS=1

# Download and build dependencies from source
deps:
	$(MAKE) -C clib deps

# Clean build artifacts
clean:
	$(MAKE) -C clib clean

# Run tests with library path set
test:
	STREAM_SDK_LIB_PATH=$(BUILD_DIR) CGO_ENABLED=0 go test -v ./...

# Run tests with CGO
test-cgo:
	CGO_ENABLED=1 go test -v ./...

# Build and test
check: build test

# Run webrtc-pattern example
run-example:
	@echo "Libraries in: $(BUILD_DIR)"
	cd examples/webrtc-pattern && STREAM_SDK_LIB_PATH=$(BUILD_DIR) CGO_ENABLED=0 go run .

# Show available targets
help:
	@echo "Available targets:"
	@echo "  all          - Build all native libraries (default)"
	@echo "  build        - Build all native libraries from source"
	@echo "  build-system - Build using system libraries (faster)"
	@echo "  deps         - Download and build dependencies from source"
	@echo "  clean        - Remove build artifacts"
	@echo "  test         - Run tests without CGO"
	@echo "  test-cgo     - Run tests with CGO"
	@echo "  check        - Build and test"
	@echo "  run-example  - Run webrtc-pattern example"
	@echo ""
	@echo "Libraries are placed in ./build/"
	@echo "Set STREAM_SDK_LIB_PATH=$(BUILD_DIR) when running outside make"

# Show build info
info:
	@$(MAKE) -C clib info
	@echo "Build directory: $(BUILD_DIR)"
	@ls -la $(BUILD_DIR)/*.dylib 2>/dev/null || ls -la $(BUILD_DIR)/*.so 2>/dev/null || echo "No libraries built yet"
