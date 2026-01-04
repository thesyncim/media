// media_avfoundation.h - Thin C wrapper for macOS AVFoundation
//
// This provides a simple C API for camera/microphone enumeration and capture
// on macOS, suitable for use with purego bindings.

#ifndef MEDIA_AVFOUNDATION_H
#define MEDIA_AVFOUNDATION_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Authorization status (matches AVAuthorizationStatus)
#define MEDIA_AV_AUTH_NOT_DETERMINED 0
#define MEDIA_AV_AUTH_RESTRICTED     1
#define MEDIA_AV_AUTH_DENIED         2
#define MEDIA_AV_AUTH_AUTHORIZED     3

// Device kinds
#define MEDIA_AV_DEVICE_VIDEO_INPUT  0
#define MEDIA_AV_DEVICE_AUDIO_INPUT  1
#define MEDIA_AV_DEVICE_AUDIO_OUTPUT 2

// Error codes
#define MEDIA_AV_OK              0
#define MEDIA_AV_ERROR          -1
#define MEDIA_AV_ERROR_PERM     -2
#define MEDIA_AV_ERROR_NOTFOUND -3
#define MEDIA_AV_ERROR_BUSY     -4

// Opaque handles
typedef uint64_t MediaAVCaptureSession;
typedef uint64_t MediaAVVideoCapture;
typedef uint64_t MediaAVAudioCapture;

// Device enumeration
int32_t media_av_video_device_count(void);
int32_t media_av_audio_input_device_count(void);

// Get device info (returns heap-allocated string that must be freed)
const char* media_av_video_device_id(int32_t index);
const char* media_av_video_device_label(int32_t index);
const char* media_av_audio_input_device_id(int32_t index);
const char* media_av_audio_input_device_label(int32_t index);

// Get video device frame rate range (returns min and max FPS * 100 for precision)
// Returns 0 on success, negative on error
int32_t media_av_video_device_fps_range(const char* device_id, int32_t* min_fps, int32_t* max_fps);

// Free string returned by above functions
void media_av_free_string(const char* str);

// Permission handling
int32_t media_av_camera_permission_status(void);
int32_t media_av_microphone_permission_status(void);
void media_av_request_camera_permission(void);
void media_av_request_microphone_permission(void);

// Video capture
// Frame callback: called for each captured frame
// data: I420 frame data (Y plane first, then U, then V)
// width, height: frame dimensions
// timestamp_ns: capture timestamp in nanoseconds
typedef void (*MediaAVFrameCallback)(
    const uint8_t* y_plane, int32_t y_stride,
    const uint8_t* u_plane, int32_t u_stride,
    const uint8_t* v_plane, int32_t v_stride,
    int32_t width, int32_t height,
    int64_t timestamp_ns,
    void* user_data
);

// Create video capture for device
// Returns handle or 0 on error
MediaAVVideoCapture media_av_video_capture_create(
    const char* device_id,
    int32_t width,
    int32_t height,
    int32_t fps,
    MediaAVFrameCallback callback,
    void* user_data
);

// Start/stop video capture
int32_t media_av_video_capture_start(MediaAVVideoCapture handle);
int32_t media_av_video_capture_stop(MediaAVVideoCapture handle);
void media_av_video_capture_destroy(MediaAVVideoCapture handle);

// Audio capture
// Audio callback: called for each captured audio buffer
typedef void (*MediaAVAudioCallback)(
    const int16_t* samples,
    int32_t sample_count,
    int32_t channels,
    int32_t sample_rate,
    int64_t timestamp_ns,
    void* user_data
);

// Create audio capture for device
MediaAVAudioCapture media_av_audio_capture_create(
    const char* device_id,
    int32_t sample_rate,
    int32_t channels,
    MediaAVAudioCallback callback,
    void* user_data
);

// Start/stop audio capture
int32_t media_av_audio_capture_start(MediaAVAudioCapture handle);
int32_t media_av_audio_capture_stop(MediaAVAudioCapture handle);
void media_av_audio_capture_destroy(MediaAVAudioCapture handle);

// Get last error message
const char* media_av_get_error(void);

#ifdef __cplusplus
}
#endif

#endif // MEDIA_AVFOUNDATION_H
