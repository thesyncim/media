// media_alsa.h - Thin C wrapper for Linux ALSA audio capture
//
// This provides a simple C API for audio device enumeration and capture
// on Linux, suitable for use with purego bindings.

#ifndef MEDIA_ALSA_H
#define MEDIA_ALSA_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Error codes
#define MEDIA_ALSA_OK              0
#define MEDIA_ALSA_ERROR          -1
#define MEDIA_ALSA_ERROR_PERM     -2
#define MEDIA_ALSA_ERROR_NOTFOUND -3
#define MEDIA_ALSA_ERROR_BUSY     -4
#define MEDIA_ALSA_ERROR_NOMEM    -5

// Opaque handle
typedef uint64_t MediaALSACapture;

// Device info structure
typedef struct MediaALSADeviceInfo {
    char* hw_id;     // e.g., "hw:0,0" or "plughw:0,0"
    char* name;      // Human-readable name
    int32_t card;    // Card number
    int32_t device;  // Device number
} MediaALSADeviceInfo;

// Audio callback: called for each captured buffer
// samples: interleaved 16-bit signed PCM samples
typedef void (*MediaALSAAudioCallback)(
    const int16_t* samples,
    int32_t sample_count,
    int32_t channels,
    int32_t sample_rate,
    int64_t timestamp_ns,
    void* user_data
);

// Device enumeration
// Returns number of audio capture devices found
int32_t media_alsa_input_device_count(void);

// Get device info (caller must free with media_alsa_free_device_info)
MediaALSADeviceInfo* media_alsa_get_input_device_info(int32_t index);
void media_alsa_free_device_info(MediaALSADeviceInfo* info);

// Get device ID and name at index (returns heap-allocated string, must free with media_alsa_free_string)
const char* media_alsa_input_device_id(int32_t index);
const char* media_alsa_input_device_name(int32_t index);
void media_alsa_free_string(const char* str);

// Create audio capture for device
// device_id: e.g., "hw:0,0" or "plughw:0,0" or NULL for default
// Returns handle or 0 on error
MediaALSACapture media_alsa_capture_create(
    const char* device_id,
    int32_t sample_rate,
    int32_t channels,
    MediaALSAAudioCallback callback,
    void* user_data
);

// Start/stop audio capture
int32_t media_alsa_capture_start(MediaALSACapture handle);
int32_t media_alsa_capture_stop(MediaALSACapture handle);
void media_alsa_capture_destroy(MediaALSACapture handle);

// Get actual capture parameters (may differ from requested)
int32_t media_alsa_capture_get_sample_rate(MediaALSACapture handle);
int32_t media_alsa_capture_get_channels(MediaALSACapture handle);

// Get last error message
const char* media_alsa_get_error(void);

#ifdef __cplusplus
}
#endif

#endif // MEDIA_ALSA_H
