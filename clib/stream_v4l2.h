// stream_v4l2.h - Thin C wrapper for Linux V4L2 video capture
//
// This provides a simple C API for camera enumeration and capture
// on Linux, suitable for use with purego bindings.

#ifndef STREAM_V4L2_H
#define STREAM_V4L2_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Error codes
#define STREAM_V4L2_OK              0
#define STREAM_V4L2_ERROR          -1
#define STREAM_V4L2_ERROR_PERM     -2
#define STREAM_V4L2_ERROR_NOTFOUND -3
#define STREAM_V4L2_ERROR_BUSY     -4
#define STREAM_V4L2_ERROR_NOMEM    -5

// Opaque handle
typedef uint64_t StreamV4L2Capture;

// Device info structure
typedef struct StreamV4L2DeviceInfo {
    char* device_path;  // e.g., "/dev/video0"
    char* name;         // Human-readable name
    int32_t index;      // Device index
} StreamV4L2DeviceInfo;

// Frame callback: called for each captured frame
// Delivers I420 format (Y plane first, then U, then V)
typedef void (*StreamV4L2FrameCallback)(
    const uint8_t* y_plane, int32_t y_stride,
    const uint8_t* u_plane, int32_t u_stride,
    const uint8_t* v_plane, int32_t v_stride,
    int32_t width, int32_t height,
    int64_t timestamp_ns,
    void* user_data
);

// Device enumeration
// Returns number of video devices found, or negative error code
int32_t stream_v4l2_device_count(void);

// Get device info (caller must free with stream_v4l2_free_device_info)
StreamV4L2DeviceInfo* stream_v4l2_get_device_info(int32_t index);
void stream_v4l2_free_device_info(StreamV4L2DeviceInfo* info);

// Get device path at index (returns heap-allocated string, must free with stream_v4l2_free_string)
const char* stream_v4l2_device_path(int32_t index);
const char* stream_v4l2_device_name(int32_t index);
void stream_v4l2_free_string(const char* str);

// Create video capture for device
// device_path: e.g., "/dev/video0" or NULL for default
// Returns handle or 0 on error
StreamV4L2Capture stream_v4l2_capture_create(
    const char* device_path,
    int32_t width,
    int32_t height,
    int32_t fps,
    StreamV4L2FrameCallback callback,
    void* user_data
);

// Start/stop video capture
int32_t stream_v4l2_capture_start(StreamV4L2Capture handle);
int32_t stream_v4l2_capture_stop(StreamV4L2Capture handle);
void stream_v4l2_capture_destroy(StreamV4L2Capture handle);

// Get actual capture dimensions (may differ from requested)
int32_t stream_v4l2_capture_get_width(StreamV4L2Capture handle);
int32_t stream_v4l2_capture_get_height(StreamV4L2Capture handle);
int32_t stream_v4l2_capture_get_fps(StreamV4L2Capture handle);

// Get last error message
const char* stream_v4l2_get_error(void);

#ifdef __cplusplus
}
#endif

#endif // STREAM_V4L2_H
