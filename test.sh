#!/bin/bash
# Cross-platform test script for media package
# Supports: macOS (native), Linux (native or Docker)
#
# Usage:
#   ./test.sh              # Run tests on current platform
#   ./test.sh docker       # Run tests in Linux Docker container
#   ./test.sh build        # Build C libraries only
#   ./test.sh all          # Build + test

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR"
CLIB_DIR="$SCRIPT_DIR/clib"
BUILD_DIR="$PROJECT_ROOT/build"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Detect platform
OS="$(uname -s)"
ARCH="$(uname -m)"

build_libs() {
    log_info "Building C libraries for $OS/$ARCH..."
    cd "$CLIB_DIR"

    if [[ "$OS" == "Darwin" ]]; then
        # macOS: Use system libs (Homebrew) by default for faster builds
        # Set USE_SYSTEM_LIBS=0 to build from source
        make USE_SYSTEM_LIBS=${USE_SYSTEM_LIBS:-1} all
    elif [[ "$OS" == "Linux" ]]; then
        # Linux: Build from source for reproducibility
        make deps
        make all
    else
        log_error "Unsupported platform: $OS"
        exit 1
    fi

    log_info "Libraries built in $BUILD_DIR"
}

run_tests() {
    log_info "Running tests on $OS/$ARCH..."

    export MEDIA_SDK_LIB_PATH="$BUILD_DIR"

    if [[ "$OS" == "Darwin" ]]; then
        export DYLD_LIBRARY_PATH="$BUILD_DIR:$DYLD_LIBRARY_PATH"
    elif [[ "$OS" == "Linux" ]]; then
        export LD_LIBRARY_PATH="$BUILD_DIR:$LD_LIBRARY_PATH"
    fi

    cd "$PROJECT_ROOT"
    go test -v ./...
}

run_docker_tests() {
    log_info "Running tests in Linux Docker container..."
    cd "$PROJECT_ROOT"

    docker build -f Dockerfile.test -t media-test .
    docker run --rm media-test
}

show_help() {
    echo "Cross-platform test script for media package"
    echo ""
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  (none)    Run tests on current platform (default)"
    echo "  build     Build C libraries only"
    echo "  test      Run tests only (assumes libs are built)"
    echo "  all       Build + test"
    echo "  docker    Run tests in Linux Docker container"
    echo "  help      Show this help"
    echo ""
    echo "Environment:"
    echo "  USE_SYSTEM_LIBS=0   Build dependencies from source (default on Linux)"
    echo "  USE_SYSTEM_LIBS=1   Use system libraries (default on macOS)"
    echo ""
    echo "Libraries built:"
    echo "  - libmedia_vpx (VP8/VP9 encoding/decoding)"
    echo "  - libmedia_opus (Opus audio encoding/decoding)"
    echo "  - libmedia_avfoundation (macOS camera/mic - macOS only)"
    echo "  - libmedia_v4l2 (Linux camera via V4L2 - Linux only)"
    echo "  - libmedia_alsa (Linux audio via ALSA - Linux only)"
    echo ""
    echo "Platform: $OS/$ARCH"
}

# Main
case "${1:-test}" in
    build)
        build_libs
        ;;
    test)
        run_tests
        ;;
    all)
        build_libs
        run_tests
        ;;
    docker)
        run_docker_tests
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        log_error "Unknown command: $1"
        show_help
        exit 1
        ;;
esac

log_info "Done!"
