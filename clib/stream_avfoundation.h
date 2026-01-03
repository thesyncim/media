// stream_avfoundation.h - Thin C wrapper for macOS AVFoundation
//
// This provides a simple C API for camera/microphone enumeration and capture
// on macOS, suitable for use with purego bindings.

#ifndef STREAM_AVFOUNDATION_H
#define STREAM_AVFOUNDATION_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Authorization status (matches AVAuthorizationStatus)
#define STREAM_AV_AUTH_NOT_DETERMINED 0
#define STREAM_AV_AUTH_RESTRICTED     1
#define STREAM_AV_AUTH_DENIED         2
#define STREAM_AV_AUTH_AUTHORIZED     3

// Device kinds
#define STREAM_AV_DEVICE_VIDEO_INPUT  0
#define STREAM_AV_DEVICE_AUDIO_INPUT  1
#define STREAM_AV_DEVICE_AUDIO_OUTPUT 2

// Error codes
#define STREAM_AV_OK              0
#define STREAM_AV_ERROR          -1
#define STREAM_AV_ERROR_PERM     -2
#define STREAM_AV_ERROR_NOTFOUND -3
#define STREAM_AV_ERROR_BUSY     -4

// Opaque handles
typedef uint64_t StreamAVCaptureSession;
typedef uint64_t StreamAVVideoCapture;
typedef uint64_t StreamAVAudioCapture;

// Device enumeration
int32_t stream_av_video_device_count(void);
int32_t stream_av_audio_input_device_count(void);

// Get device info (returns heap-allocated string that must be freed)
const char* stream_av_video_device_id(int32_t index);
const char* stream_av_video_device_label(int32_t index);
const char* stream_av_audio_input_device_id(int32_t index);
const char* stream_av_audio_input_device_label(int32_t index);

// Free string returned by above functions
void stream_av_free_string(const char* str);

// Permission handling
int32_t stream_av_camera_permission_status(void);
int32_t stream_av_microphone_permission_status(void);
void stream_av_request_camera_permission(void);
void stream_av_request_microphone_permission(void);

// Video capture
// Frame callback: called for each captured frame
// data: I420 frame data (Y plane first, then U, then V)
// width, height: frame dimensions
// timestamp_ns: capture timestamp in nanoseconds
typedef void (*StreamAVFrameCallback)(
    const uint8_t* y_plane, int32_t y_stride,
    const uint8_t* u_plane, int32_t u_stride,
    const uint8_t* v_plane, int32_t v_stride,
    int32_t width, int32_t height,
    int64_t timestamp_ns,
    void* user_data
);

// Create video capture for device
// Returns handle or 0 on error
StreamAVVideoCapture stream_av_video_capture_create(
    const char* device_id,
    int32_t width,
    int32_t height,
    int32_t fps,
    StreamAVFrameCallback callback,
    void* user_data
);

// Start/stop video capture
int32_t stream_av_video_capture_start(StreamAVVideoCapture handle);
int32_t stream_av_video_capture_stop(StreamAVVideoCapture handle);
void stream_av_video_capture_destroy(StreamAVVideoCapture handle);

// Audio capture
// Audio callback: called for each captured audio buffer
typedef void (*StreamAVAudioCallback)(
    const int16_t* samples,
    int32_t sample_count,
    int32_t channels,
    int32_t sample_rate,
    int64_t timestamp_ns,
    void* user_data
);

// Create audio capture for device
StreamAVAudioCapture stream_av_audio_capture_create(
    const char* device_id,
    int32_t sample_rate,
    int32_t channels,
    StreamAVAudioCallback callback,
    void* user_data
);

// Start/stop audio capture
int32_t stream_av_audio_capture_start(StreamAVAudioCapture handle);
int32_t stream_av_audio_capture_stop(StreamAVAudioCapture handle);
void stream_av_audio_capture_destroy(StreamAVAudioCapture handle);

// Get last error message
const char* stream_av_get_error(void);

#ifdef __cplusplus
}
#endif

#endif // STREAM_AVFOUNDATION_H
