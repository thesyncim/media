# Media Media C Wrappers - Reproducible Build
#
# This builds thin wrapper libraries for libvpx, libopus, x264, and libaom
# with simple primitive-only APIs, suitable for both purego and CGO bindings.
#
# Features:
# - Downloads and builds dependencies from source for reproducibility
# - Pins specific versions for consistent builds
# - Can use system libraries as fallback

.PHONY: all clean install help vpx opus h264 av1 deps deps-vpx deps-opus deps-x264 deps-aom \
        clean-deps distclean info check-deps \
        v4l2 alsa avfoundation compositor platform-libs

# Versions (pinned for reproducibility)
LIBVPX_VERSION := 1.15.0
LIBOPUS_VERSION := 1.5.2
X264_VERSION := stable
LIBAOM_VERSION := 3.8.1

# SHA256 checksums for reproducibility
LIBVPX_SHA256 := e935eded7d81631a538bfae703fd1e293aad1c7fd3407ba00440c95105d2011e
LIBOPUS_SHA256 := 65c1d2f78b9f2fb20082c38cbe47c951ad5839345876e46941612ee87f9a7ce1
# x264 stable branch doesn't have fixed releases, use git clone
LIBAOM_SHA256 := dedc65060812a7df801c0270a2fe8bd773c6bb0b601f2144ecfbc62dc0f671ca

# Download URLs
LIBVPX_URL := https://github.com/webmproject/libvpx/archive/refs/tags/v$(LIBVPX_VERSION).tar.gz
LIBOPUS_URL := https://downloads.xiph.org/releases/opus/opus-$(LIBOPUS_VERSION).tar.gz
X264_URL := https://code.videolan.org/videolan/x264.git
LIBAOM_URL := https://storage.googleapis.com/aom-releases/libaom-$(LIBAOM_VERSION).tar.gz

# Directories
BUILD_DIR := .deps
LIBVPX_DIR := $(BUILD_DIR)/libvpx-$(LIBVPX_VERSION)
LIBOPUS_DIR := $(BUILD_DIR)/opus-$(LIBOPUS_VERSION)
X264_DIR := $(BUILD_DIR)/x264
LIBAOM_DIR := $(BUILD_DIR)/libaom-$(LIBAOM_VERSION)
LIBAOM_BUILD_DIR := $(BUILD_DIR)/libaom-build
OUTPUT_DIR := ../build

# Detect OS and architecture
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

# Common flags
CFLAGS_COMMON := -shared -fPIC -O2

# Configuration based on USE_SYSTEM_LIBS
# Set USE_SYSTEM_LIBS=1 to use homebrew/system libraries instead of building from source
USE_SYSTEM_LIBS ?= 0

ifeq ($(UNAME_S),Linux)
    VPX_LIB := libmedia_vpx.so
    OPUS_LIB := libmedia_opus.so
    H264_LIB := libmedia_h264.so
    AV1_LIB := libmedia_av1.so
    DYLIB_EXT := so

    ifeq ($(USE_SYSTEM_LIBS),1)
        VPX_CFLAGS := $(CFLAGS_COMMON)
        VPX_LDFLAGS := -lvpx -lm
        OPUS_CFLAGS := $(CFLAGS_COMMON)
        OPUS_LDFLAGS := -lopus -lm
        # H264: x264 for encoder, OpenH264 for decoder (loaded dynamically via dlopen)
        H264_CFLAGS := $(CFLAGS_COMMON)
        H264_LDFLAGS := -lx264 -lm -lpthread -ldl
        AV1_CFLAGS := $(CFLAGS_COMMON)
        AV1_LDFLAGS := -laom -lm -lpthread
    else
        # Use locally built libraries
        LOCAL_VPX_LIB := $(LIBVPX_DIR)/libvpx.a
        LOCAL_OPUS_LIB := $(LIBOPUS_DIR)/.libs/libopus.a
        LOCAL_X264_LIB := $(X264_DIR)/libx264.a
        LOCAL_AOM_LIB := $(LIBAOM_BUILD_DIR)/libaom.a
        VPX_CFLAGS := $(CFLAGS_COMMON) -I$(LIBVPX_DIR)
        VPX_LDFLAGS := $(LOCAL_VPX_LIB) -lm -lpthread
        OPUS_CFLAGS := $(CFLAGS_COMMON) -I$(LIBOPUS_DIR)/include
        OPUS_LDFLAGS := $(LOCAL_OPUS_LIB) -lm
        # H264: x264 static lib for encoder, OpenH264 for decoder (loaded dynamically via dlopen)
        H264_CFLAGS := $(CFLAGS_COMMON) -I$(X264_DIR)
        H264_LDFLAGS := $(LOCAL_X264_LIB) -lm -lpthread -ldl
        AV1_CFLAGS := $(CFLAGS_COMMON) -I$(LIBAOM_DIR)
        AV1_LDFLAGS := $(LOCAL_AOM_LIB) -lm -lpthread
    endif
endif

ifeq ($(UNAME_S),Darwin)
    VPX_LIB := libmedia_vpx.dylib
    OPUS_LIB := libmedia_opus.dylib
    H264_LIB := libmedia_h264.dylib
    AV1_LIB := libmedia_av1.dylib
    DYLIB_EXT := dylib

    ifeq ($(USE_SYSTEM_LIBS),1)
        # Use Homebrew libraries
        HOMEBREW_PREFIX := $(shell brew --prefix 2>/dev/null || echo /opt/homebrew)
        VPX_PREFIX := $(shell brew --prefix libvpx 2>/dev/null || echo $(HOMEBREW_PREFIX)/opt/libvpx)
        OPUS_PREFIX := $(shell brew --prefix opus 2>/dev/null || echo $(HOMEBREW_PREFIX)/opt/opus)
        X264_PREFIX := $(shell brew --prefix x264 2>/dev/null || echo $(HOMEBREW_PREFIX)/opt/x264)
        AOM_PREFIX := $(shell brew --prefix aom 2>/dev/null || echo $(HOMEBREW_PREFIX)/opt/aom)

        VPX_INCLUDE ?= $(VPX_PREFIX)/include
        VPX_LIB_PATH ?= $(VPX_PREFIX)/lib
        OPUS_INCLUDE ?= $(OPUS_PREFIX)/include
        OPUS_LIB_PATH ?= $(OPUS_PREFIX)/lib
        X264_INCLUDE ?= $(X264_PREFIX)/include
        X264_LIB_PATH ?= $(X264_PREFIX)/lib
        AOM_INCLUDE ?= $(AOM_PREFIX)/include
        AOM_LIB_PATH ?= $(AOM_PREFIX)/lib

        VPX_CFLAGS := $(CFLAGS_COMMON) -I$(VPX_INCLUDE)
        VPX_LDFLAGS := -L$(VPX_LIB_PATH) -Wl,-rpath,$(VPX_LIB_PATH) -lvpx \
                       -Wl,-install_name,@rpath/libmedia_vpx.dylib
        OPUS_CFLAGS := $(CFLAGS_COMMON) -I$(OPUS_INCLUDE)
        OPUS_LDFLAGS := -L$(OPUS_LIB_PATH) -Wl,-rpath,$(OPUS_LIB_PATH) -lopus \
                        -Wl,-install_name,@rpath/libmedia_opus.dylib
        # H264: x264 for encoder, OpenH264 for decoder (loaded dynamically via dlopen)
        H264_CFLAGS := $(CFLAGS_COMMON) -I$(X264_INCLUDE)
        H264_LDFLAGS := -L$(X264_LIB_PATH) -Wl,-rpath,$(X264_LIB_PATH) -lx264 \
                        -Wl,-install_name,@rpath/libmedia_h264.dylib
        AV1_CFLAGS := $(CFLAGS_COMMON) -I$(AOM_INCLUDE)
        AV1_LDFLAGS := -L$(AOM_LIB_PATH) -Wl,-rpath,$(AOM_LIB_PATH) -laom \
                       -Wl,-install_name,@rpath/libmedia_av1.dylib
    else
        # Use locally built static libraries (fully self-contained)
        LOCAL_VPX_LIB := $(LIBVPX_DIR)/libvpx.a
        LOCAL_OPUS_LIB := $(LIBOPUS_DIR)/.libs/libopus.a
        LOCAL_X264_LIB := $(X264_DIR)/libx264.a
        LOCAL_AOM_LIB := $(LIBAOM_BUILD_DIR)/libaom.a

        VPX_CFLAGS := $(CFLAGS_COMMON) -I$(LIBVPX_DIR)
        VPX_LDFLAGS := $(LOCAL_VPX_LIB) -lm -lpthread \
                       -Wl,-install_name,@rpath/libmedia_vpx.dylib
        OPUS_CFLAGS := $(CFLAGS_COMMON) -I$(LIBOPUS_DIR)/include
        OPUS_LDFLAGS := $(LOCAL_OPUS_LIB) -lm \
                        -Wl,-install_name,@rpath/libmedia_opus.dylib
        # H264: x264 static lib for encoder, OpenH264 for decoder (loaded dynamically via dlopen)
        H264_CFLAGS := $(CFLAGS_COMMON) -I$(X264_DIR)
        H264_LDFLAGS := $(LOCAL_X264_LIB) -lm -lpthread \
                        -Wl,-install_name,@rpath/libmedia_h264.dylib
        AV1_CFLAGS := $(CFLAGS_COMMON) -I$(LIBAOM_DIR)
        AV1_LDFLAGS := $(LOCAL_AOM_LIB) -lm -lpthread \
                       -Wl,-install_name,@rpath/libmedia_av1.dylib
    endif
endif

# Number of parallel jobs for building dependencies
JOBS := $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)

# ==============================================================================
# Main Targets
# ==============================================================================

# Default target - build all libraries
all: vpx opus h264 av1 compositor platform-libs

# Check if we need to build dependencies
ifeq ($(USE_SYSTEM_LIBS),0)
vpx: deps-vpx $(OUTPUT_DIR)/$(VPX_LIB)
opus: deps-opus $(OUTPUT_DIR)/$(OPUS_LIB)
h264: deps-x264 $(OUTPUT_DIR)/$(H264_LIB)
av1: deps-aom $(OUTPUT_DIR)/$(AV1_LIB)
else
vpx: $(OUTPUT_DIR)/$(VPX_LIB)
opus: $(OUTPUT_DIR)/$(OPUS_LIB)
h264: $(OUTPUT_DIR)/$(H264_LIB)
av1: $(OUTPUT_DIR)/$(AV1_LIB)
endif

# ==============================================================================
# Platform-specific device capture libraries
# ==============================================================================

# Platform-libs target builds the appropriate device capture library
ifeq ($(UNAME_S),Darwin)
platform-libs: avfoundation
else ifeq ($(UNAME_S),Linux)
platform-libs: v4l2 alsa
else
platform-libs:
	@echo "No platform-specific libraries for $(UNAME_S)"
endif

# AVFoundation wrapper (macOS only)
ifeq ($(UNAME_S),Darwin)
AVFOUNDATION_LIB := libmedia_avfoundation.dylib
avfoundation: $(OUTPUT_DIR)/$(AVFOUNDATION_LIB)

$(OUTPUT_DIR)/$(AVFOUNDATION_LIB): media_avfoundation.m media_avfoundation.h | $(OUTPUT_DIR)
	@echo "Building $(AVFOUNDATION_LIB)..."
	clang -shared -fPIC -O2 -fobjc-arc \
		-framework AVFoundation -framework CoreMedia -framework CoreVideo -framework Foundation \
		-Wl,-install_name,@rpath/libmedia_avfoundation.dylib \
		-o $@ media_avfoundation.m
	cp media_avfoundation.h $(OUTPUT_DIR)/
	@echo "Built: $@"
else
avfoundation:
	@echo "AVFoundation is only available on macOS"
endif

# Compositor library (cross-platform)
ifeq ($(UNAME_S),Darwin)
COMPOSITOR_LIB := libmedia_compositor.dylib
compositor: $(OUTPUT_DIR)/$(COMPOSITOR_LIB)

$(OUTPUT_DIR)/$(COMPOSITOR_LIB): media_compositor.c media_compositor.h | $(OUTPUT_DIR)
	@echo "Building $(COMPOSITOR_LIB)..."
	$(CC) -shared -fPIC -O3 \
		-Wl,-install_name,@rpath/libmedia_compositor.dylib \
		-o $@ media_compositor.c
	cp media_compositor.h $(OUTPUT_DIR)/
	@echo "Built: $@"
else ifeq ($(UNAME_S),Linux)
COMPOSITOR_LIB := libmedia_compositor.so
compositor: $(OUTPUT_DIR)/$(COMPOSITOR_LIB)

$(OUTPUT_DIR)/$(COMPOSITOR_LIB): media_compositor.c media_compositor.h | $(OUTPUT_DIR)
	@echo "Building $(COMPOSITOR_LIB)..."
	$(CC) -shared -fPIC -O3 -o $@ media_compositor.c -lm
	cp media_compositor.h $(OUTPUT_DIR)/
	@echo "Built: $@"
endif

# V4L2 wrapper (Linux only)
ifeq ($(UNAME_S),Linux)
V4L2_LIB := libmedia_v4l2.so
v4l2: $(OUTPUT_DIR)/$(V4L2_LIB)

$(OUTPUT_DIR)/$(V4L2_LIB): media_v4l2.c media_v4l2.h | $(OUTPUT_DIR)
	@echo "Building $(V4L2_LIB)..."
	$(CC) $(CFLAGS_COMMON) -o $@ media_v4l2.c -lpthread
	cp media_v4l2.h $(OUTPUT_DIR)/
	@echo "Built: $@"
else
v4l2:
	@echo "V4L2 is only available on Linux"
endif

# ALSA wrapper (Linux only)
ifeq ($(UNAME_S),Linux)
ALSA_LIB := libmedia_alsa.so
alsa: $(OUTPUT_DIR)/$(ALSA_LIB)

$(OUTPUT_DIR)/$(ALSA_LIB): media_alsa.c media_alsa.h | $(OUTPUT_DIR)
	@echo "Building $(ALSA_LIB)..."
	$(CC) $(CFLAGS_COMMON) -o $@ media_alsa.c -lasound -lpthread
	cp media_alsa.h $(OUTPUT_DIR)/
	@echo "Built: $@"
else
alsa:
	@echo "ALSA is only available on Linux"
endif

# Create output directory
$(OUTPUT_DIR):
	mkdir -p $(OUTPUT_DIR)

# ==============================================================================
# Build VPX Wrapper
# ==============================================================================

$(OUTPUT_DIR)/$(VPX_LIB): media_vpx.c media_vpx.h | $(OUTPUT_DIR)
	@echo "Building $(VPX_LIB)..."
ifeq ($(USE_SYSTEM_LIBS),0)
	@test -f $(LOCAL_VPX_LIB) || (echo "Error: libvpx not built. Run 'make deps-vpx' first." && exit 1)
endif
	$(CC) $(VPX_CFLAGS) -o $@ media_vpx.c $(VPX_LDFLAGS)
ifeq ($(UNAME_S),Darwin)
ifeq ($(USE_SYSTEM_LIBS),1)
	@# Fix Homebrew placeholder paths
	@install_name_tool -change "@@HOMEBREW_PREFIX@@/opt/libvpx/lib/libvpx.11.dylib" \
		"$(VPX_LIB_PATH)/libvpx.11.dylib" $@ 2>/dev/null || true
endif
endif
	cp media_vpx.h $(OUTPUT_DIR)/
	@echo "Built: $@"

# ==============================================================================
# Build Opus Wrapper
# ==============================================================================

$(OUTPUT_DIR)/$(OPUS_LIB): media_opus.c media_opus.h | $(OUTPUT_DIR)
	@echo "Building $(OPUS_LIB)..."
ifeq ($(USE_SYSTEM_LIBS),0)
	@test -f $(LOCAL_OPUS_LIB) || (echo "Error: libopus not built. Run 'make deps-opus' first." && exit 1)
endif
	$(CC) $(OPUS_CFLAGS) -o $@ media_opus.c $(OPUS_LDFLAGS)
ifeq ($(UNAME_S),Darwin)
ifeq ($(USE_SYSTEM_LIBS),1)
	@# Fix Homebrew placeholder paths
	@install_name_tool -change "@@HOMEBREW_PREFIX@@/opt/opus/lib/libopus.0.dylib" \
		"$(OPUS_LIB_PATH)/libopus.0.dylib" $@ 2>/dev/null || true
endif
endif
	cp media_opus.h $(OUTPUT_DIR)/
	@echo "Built: $@"

# ==============================================================================
# Build H.264 Wrapper (x264)
# ==============================================================================

$(OUTPUT_DIR)/$(H264_LIB): media_h264.c media_h264.h | $(OUTPUT_DIR)
	@echo "Building $(H264_LIB)..."
ifeq ($(USE_SYSTEM_LIBS),0)
	@test -f $(LOCAL_X264_LIB) || (echo "Error: x264 not built. Run 'make deps-x264' first." && exit 1)
endif
	$(CC) $(H264_CFLAGS) -o $@ media_h264.c $(H264_LDFLAGS)
ifeq ($(UNAME_S),Darwin)
ifeq ($(USE_SYSTEM_LIBS),1)
	@# Fix Homebrew placeholder paths (try multiple versions)
	@install_name_tool -change "@@HOMEBREW_PREFIX@@/opt/x264/lib/libx264.165.dylib" \
		"$(X264_LIB_PATH)/libx264.165.dylib" $@ 2>/dev/null || true
	@install_name_tool -change "@@HOMEBREW_PREFIX@@/opt/x264/lib/libx264.164.dylib" \
		"$(X264_LIB_PATH)/libx264.164.dylib" $@ 2>/dev/null || true
endif
endif
	cp media_h264.h $(OUTPUT_DIR)/
	@echo "Built: $@"

# ==============================================================================
# Build AV1 Wrapper (libaom)
# ==============================================================================

$(OUTPUT_DIR)/$(AV1_LIB): media_av1.c media_av1.h | $(OUTPUT_DIR)
	@echo "Building $(AV1_LIB)..."
ifeq ($(USE_SYSTEM_LIBS),0)
	@test -f $(LOCAL_AOM_LIB) || (echo "Error: libaom not built. Run 'make deps-aom' first." && exit 1)
endif
	$(CC) $(AV1_CFLAGS) -o $@ media_av1.c $(AV1_LDFLAGS)
ifeq ($(UNAME_S),Darwin)
ifeq ($(USE_SYSTEM_LIBS),1)
	@# Fix Homebrew placeholder paths
	@install_name_tool -change "@@HOMEBREW_PREFIX@@/opt/aom/lib/libaom.3.dylib" \
		"$(AOM_LIB_PATH)/libaom.3.dylib" $@ 2>/dev/null || true
endif
endif
	cp media_av1.h $(OUTPUT_DIR)/
	@echo "Built: $@"

# ==============================================================================
# Dependency Building (for reproducible builds)
# ==============================================================================

deps: deps-vpx deps-opus deps-x264 deps-aom

# Download and build libvpx
deps-vpx: $(LOCAL_VPX_LIB)

$(LOCAL_VPX_LIB): $(LIBVPX_DIR)/Makefile
	@echo "Building libvpx $(LIBVPX_VERSION)..."
	cd $(LIBVPX_DIR) && $(MAKE) -j$(JOBS)
	@echo "libvpx built successfully."

$(LIBVPX_DIR)/Makefile: $(LIBVPX_DIR)/configure
	@echo "Configuring libvpx $(LIBVPX_VERSION)..."
	cd $(LIBVPX_DIR) && ./configure \
		--disable-examples \
		--disable-tools \
		--disable-docs \
		--disable-unit-tests \
		--enable-vp8 \
		--enable-vp9 \
		--enable-vp9-temporal-denoising \
		--enable-vp9-postproc \
		--enable-multithread \
		--enable-runtime-cpu-detect \
		--enable-pic \
		--enable-static \
		--disable-shared

$(LIBVPX_DIR)/configure: | $(BUILD_DIR)
	@echo "Downloading libvpx $(LIBVPX_VERSION)..."
	curl -L -o $(BUILD_DIR)/libvpx-$(LIBVPX_VERSION).tar.gz $(LIBVPX_URL)
	@echo "Verifying checksum..."
	@echo "$(LIBVPX_SHA256)  $(BUILD_DIR)/libvpx-$(LIBVPX_VERSION).tar.gz" | shasum -a 256 -c -
	cd $(BUILD_DIR) && tar xzf libvpx-$(LIBVPX_VERSION).tar.gz
	@rm $(BUILD_DIR)/libvpx-$(LIBVPX_VERSION).tar.gz
	@echo "libvpx source extracted."

# Download and build libopus
deps-opus: $(LOCAL_OPUS_LIB)

$(LOCAL_OPUS_LIB): $(LIBOPUS_DIR)/Makefile
	@echo "Building libopus $(LIBOPUS_VERSION)..."
	cd $(LIBOPUS_DIR) && $(MAKE) -j$(JOBS)
	@# Create opus/ subdirectory with headers for standard include path
	@mkdir -p $(LIBOPUS_DIR)/include/opus
	@cp $(LIBOPUS_DIR)/include/*.h $(LIBOPUS_DIR)/include/opus/ 2>/dev/null || true
	@echo "libopus built successfully."

$(LIBOPUS_DIR)/Makefile: $(LIBOPUS_DIR)/configure
	@echo "Configuring libopus $(LIBOPUS_VERSION)..."
	cd $(LIBOPUS_DIR) && ./configure \
		--disable-doc \
		--disable-extra-programs \
		--enable-static \
		--disable-shared

$(LIBOPUS_DIR)/configure: | $(BUILD_DIR)
	@echo "Downloading libopus $(LIBOPUS_VERSION)..."
	curl -L -o $(BUILD_DIR)/opus-$(LIBOPUS_VERSION).tar.gz $(LIBOPUS_URL)
	@echo "Verifying checksum..."
	@echo "$(LIBOPUS_SHA256)  $(BUILD_DIR)/opus-$(LIBOPUS_VERSION).tar.gz" | shasum -a 256 -c -
	cd $(BUILD_DIR) && tar xzf opus-$(LIBOPUS_VERSION).tar.gz
	@rm $(BUILD_DIR)/opus-$(LIBOPUS_VERSION).tar.gz
	@echo "libopus source extracted."

# Download and build x264
deps-x264: $(LOCAL_X264_LIB)

$(LOCAL_X264_LIB): $(X264_DIR)/Makefile
	@echo "Building x264..."
	cd $(X264_DIR) && $(MAKE) -j$(JOBS)
	@echo "x264 built successfully."

$(X264_DIR)/Makefile: $(X264_DIR)/configure
	@echo "Configuring x264..."
	cd $(X264_DIR) && ./configure \
		--disable-cli \
		--enable-static \
		--disable-shared \
		--enable-pic \
		--disable-lavf \
		--disable-swscale \
		--disable-ffms \
		--disable-gpac

$(X264_DIR)/configure: | $(BUILD_DIR)
	@echo "Cloning x264 (stable branch)..."
	git clone --depth 1 --branch $(X264_VERSION) $(X264_URL) $(X264_DIR)
	@echo "x264 source cloned."

# Download and build libaom
deps-aom: $(LOCAL_AOM_LIB)

$(LOCAL_AOM_LIB): $(LIBAOM_BUILD_DIR)/Makefile
	@echo "Building libaom $(LIBAOM_VERSION)..."
	cd $(LIBAOM_BUILD_DIR) && $(MAKE) -j$(JOBS)
	@echo "libaom built successfully."

$(LIBAOM_BUILD_DIR)/Makefile: $(LIBAOM_DIR)/CMakeLists.txt
	@echo "Configuring libaom $(LIBAOM_VERSION)..."
	mkdir -p $(LIBAOM_BUILD_DIR)
	cd $(LIBAOM_BUILD_DIR) && cmake $(realpath $(LIBAOM_DIR)) \
		-DCMAKE_BUILD_TYPE=Release \
		-DENABLE_DOCS=0 \
		-DENABLE_EXAMPLES=0 \
		-DENABLE_TESTDATA=0 \
		-DENABLE_TESTS=0 \
		-DENABLE_TOOLS=0 \
		-DBUILD_SHARED_LIBS=0 \
		-DCONFIG_PIC=1 \
		-DCONFIG_AV1_ENCODER=1 \
		-DCONFIG_AV1_DECODER=1 \
		-DCONFIG_REALTIME_ONLY=1

$(LIBAOM_DIR)/CMakeLists.txt: | $(BUILD_DIR)
	@echo "Downloading libaom $(LIBAOM_VERSION)..."
	curl -L -o $(BUILD_DIR)/libaom-$(LIBAOM_VERSION).tar.gz $(LIBAOM_URL)
	@echo "Verifying checksum..."
	@echo "$(LIBAOM_SHA256)  $(BUILD_DIR)/libaom-$(LIBAOM_VERSION).tar.gz" | shasum -a 256 -c -
	mkdir -p $(LIBAOM_DIR)
	cd $(BUILD_DIR) && tar xzf libaom-$(LIBAOM_VERSION).tar.gz
	@rm $(BUILD_DIR)/libaom-$(LIBAOM_VERSION).tar.gz
	@echo "libaom source extracted."

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

# ==============================================================================
# Utility Targets
# ==============================================================================

# Install to system (requires sudo on most systems)
install: all
ifeq ($(UNAME_S),Linux)
	sudo cp $(OUTPUT_DIR)/$(VPX_LIB) $(OUTPUT_DIR)/$(OPUS_LIB) \
	       $(OUTPUT_DIR)/$(H264_LIB) $(OUTPUT_DIR)/$(AV1_LIB) /usr/local/lib/
	sudo cp media_vpx.h media_opus.h media_h264.h media_av1.h /usr/local/include/
	sudo ldconfig
endif
ifeq ($(UNAME_S),Darwin)
	cp $(OUTPUT_DIR)/$(VPX_LIB) $(OUTPUT_DIR)/$(OPUS_LIB) \
	   $(OUTPUT_DIR)/$(H264_LIB) $(OUTPUT_DIR)/$(AV1_LIB) /usr/local/lib/
	cp media_vpx.h media_opus.h media_h264.h media_av1.h /usr/local/include/
endif

# Clean wrapper libraries only
clean:
	rm -f $(OUTPUT_DIR)/$(VPX_LIB) $(OUTPUT_DIR)/$(OPUS_LIB)
	rm -f $(OUTPUT_DIR)/$(H264_LIB) $(OUTPUT_DIR)/$(AV1_LIB)
	rm -f $(OUTPUT_DIR)/media_vpx.h $(OUTPUT_DIR)/media_opus.h
	rm -f $(OUTPUT_DIR)/media_h264.h $(OUTPUT_DIR)/media_av1.h
ifeq ($(UNAME_S),Darwin)
	rm -f $(OUTPUT_DIR)/libmedia_avfoundation.dylib $(OUTPUT_DIR)/media_avfoundation.h
endif
ifeq ($(UNAME_S),Linux)
	rm -f $(OUTPUT_DIR)/libmedia_v4l2.so $(OUTPUT_DIR)/media_v4l2.h
	rm -f $(OUTPUT_DIR)/libmedia_alsa.so $(OUTPUT_DIR)/media_alsa.h
endif

# Clean downloaded dependencies
clean-deps:
	rm -rf $(BUILD_DIR)

# Full clean (everything)
distclean: clean clean-deps

# Show build info
info:
	@echo "Media Media C Wrappers Build Info"
	@echo "=================================="
	@echo "OS:              $(UNAME_S)"
	@echo "Architecture:    $(UNAME_M)"
	@echo "Use system libs: $(USE_SYSTEM_LIBS)"
	@echo ""
	@echo "Versions:"
	@echo "  libvpx:        $(LIBVPX_VERSION)"
	@echo "  libopus:       $(LIBOPUS_VERSION)"
	@echo "  x264:          $(X264_VERSION)"
	@echo "  libaom:        $(LIBAOM_VERSION)"
	@echo ""
	@echo "Output:"
	@echo "  VPX lib:       $(OUTPUT_DIR)/$(VPX_LIB)"
	@echo "  Opus lib:      $(OUTPUT_DIR)/$(OPUS_LIB)"
	@echo "  H264 lib:      $(OUTPUT_DIR)/$(H264_LIB)"
	@echo "  AV1 lib:       $(OUTPUT_DIR)/$(AV1_LIB)"
ifeq ($(USE_SYSTEM_LIBS),0)
	@echo ""
	@echo "Dependencies will be built from source in:"
	@echo "  libvpx:        $(LIBVPX_DIR)"
	@echo "  libopus:       $(LIBOPUS_DIR)"
	@echo "  x264:          $(X264_DIR)"
	@echo "  libaom:        $(LIBAOM_DIR)"
else
	@echo ""
	@echo "Using system libraries:"
ifeq ($(UNAME_S),Darwin)
	@echo "  VPX include:   $(VPX_INCLUDE)"
	@echo "  VPX lib:       $(VPX_LIB_PATH)"
	@echo "  Opus include:  $(OPUS_INCLUDE)"
	@echo "  Opus lib:      $(OPUS_LIB_PATH)"
	@echo "  x264 include:  $(X264_INCLUDE)"
	@echo "  x264 lib:      $(X264_LIB_PATH)"
	@echo "  aom include:   $(AOM_INCLUDE)"
	@echo "  aom lib:       $(AOM_LIB_PATH)"
endif
endif

# Check if dependencies are available
check-deps:
ifeq ($(USE_SYSTEM_LIBS),0)
	@echo "Checking local dependencies..."
	@test -f $(LOCAL_VPX_LIB) && echo "✓ libvpx.a found" || echo "✗ libvpx.a not found (run 'make deps-vpx')"
	@test -f $(LOCAL_OPUS_LIB) && echo "✓ libopus.a found" || echo "✗ libopus.a not found (run 'make deps-opus')"
	@test -f $(LOCAL_X264_LIB) && echo "✓ libx264.a found" || echo "✗ libx264.a not found (run 'make deps-x264')"
	@test -f $(LOCAL_AOM_LIB) && echo "✓ libaom.a found" || echo "✗ libaom.a not found (run 'make deps-aom')"
else
	@echo "Checking system dependencies..."
ifeq ($(UNAME_S),Darwin)
	@test -f $(VPX_LIB_PATH)/libvpx.dylib && echo "✓ libvpx found" || echo "✗ libvpx not found"
	@test -f $(OPUS_LIB_PATH)/libopus.dylib && echo "✓ libopus found" || echo "✗ libopus not found"
	@test -f $(X264_LIB_PATH)/libx264.dylib && echo "✓ libx264 found" || echo "✗ libx264 not found"
	@test -f $(AOM_LIB_PATH)/libaom.dylib && echo "✓ libaom found" || echo "✗ libaom not found"
else
	@pkg-config --exists vpx && echo "✓ libvpx found" || echo "✗ libvpx not found"
	@pkg-config --exists opus && echo "✓ libopus found" || echo "✗ libopus not found"
	@pkg-config --exists x264 && echo "✓ libx264 found" || echo "✗ libx264 not found"
	@pkg-config --exists aom && echo "✓ libaom found" || echo "✗ libaom not found"
endif
endif

# Help
help:
	@echo "Media Media C Wrappers - Reproducible Build System"
	@echo ""
	@echo "This Makefile can build libvpx, libopus, x264, and libaom from source"
	@echo "for fully reproducible builds, or use system-installed libraries."
	@echo ""
	@echo "Main Targets:"
	@echo "  all          - Build all wrapper libraries (default)"
	@echo "  vpx          - Build libmedia_vpx only (VP8/VP9)"
	@echo "  opus         - Build libmedia_opus only (audio)"
	@echo "  h264         - Build libmedia_h264 only (H.264)"
	@echo "  av1          - Build libmedia_av1 only (AV1)"
	@echo "  deps         - Download and build all dependencies from source"
	@echo "  deps-vpx     - Download and build libvpx from source"
	@echo "  deps-opus    - Download and build libopus from source"
	@echo "  deps-x264    - Download and build x264 from source"
	@echo "  deps-aom     - Download and build libaom from source"
	@echo ""
	@echo "Platform-specific Targets:"
	@echo "  platform-libs - Build platform-specific device capture libraries"
	@echo "  avfoundation  - Build AVFoundation wrapper (macOS only)"
	@echo "  v4l2          - Build V4L2 camera wrapper (Linux only)"
	@echo "  alsa          - Build ALSA audio wrapper (Linux only)"
	@echo ""
	@echo "Utility Targets:"
	@echo "  install      - Install to system paths"
	@echo "  clean        - Remove wrapper libraries"
	@echo "  clean-deps   - Remove downloaded dependencies"
	@echo "  distclean    - Remove everything (clean + clean-deps)"
	@echo "  info         - Show build configuration"
	@echo "  check-deps   - Check if dependencies are available"
	@echo "  help         - Show this help"
	@echo ""
	@echo "Options:"
	@echo "  USE_SYSTEM_LIBS=1  - Use system libraries instead of building from source"
	@echo ""
	@echo "Examples:"
	@echo "  make                        # Build from source (reproducible)"
	@echo "  make USE_SYSTEM_LIBS=1      # Use homebrew/system libraries"
	@echo "  make deps                   # Only download and build dependencies"
	@echo "  make info                   # Show build configuration"
	@echo ""
	@echo "Pinned Versions:"
	@echo "  libvpx:  $(LIBVPX_VERSION)"
	@echo "  libopus: $(LIBOPUS_VERSION)"
	@echo "  x264:    $(X264_VERSION)"
	@echo "  libaom:  $(LIBAOM_VERSION)"
