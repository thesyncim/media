// stream_alsa.h - Thin C wrapper for Linux ALSA audio capture
//
// This provides a simple C API for audio device enumeration and capture
// on Linux, suitable for use with purego bindings.

#ifndef STREAM_ALSA_H
#define STREAM_ALSA_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Error codes
#define STREAM_ALSA_OK              0
#define STREAM_ALSA_ERROR          -1
#define STREAM_ALSA_ERROR_PERM     -2
#define STREAM_ALSA_ERROR_NOTFOUND -3
#define STREAM_ALSA_ERROR_BUSY     -4
#define STREAM_ALSA_ERROR_NOMEM    -5

// Opaque handle
typedef uint64_t StreamALSACapture;

// Device info structure
typedef struct StreamALSADeviceInfo {
    char* hw_id;     // e.g., "hw:0,0" or "plughw:0,0"
    char* name;      // Human-readable name
    int32_t card;    // Card number
    int32_t device;  // Device number
} StreamALSADeviceInfo;

// Audio callback: called for each captured buffer
// samples: interleaved 16-bit signed PCM samples
typedef void (*StreamALSAAudioCallback)(
    const int16_t* samples,
    int32_t sample_count,
    int32_t channels,
    int32_t sample_rate,
    int64_t timestamp_ns,
    void* user_data
);

// Device enumeration
// Returns number of audio capture devices found
int32_t stream_alsa_input_device_count(void);

// Get device info (caller must free with stream_alsa_free_device_info)
StreamALSADeviceInfo* stream_alsa_get_input_device_info(int32_t index);
void stream_alsa_free_device_info(StreamALSADeviceInfo* info);

// Get device ID and name at index (returns heap-allocated string, must free with stream_alsa_free_string)
const char* stream_alsa_input_device_id(int32_t index);
const char* stream_alsa_input_device_name(int32_t index);
void stream_alsa_free_string(const char* str);

// Create audio capture for device
// device_id: e.g., "hw:0,0" or "plughw:0,0" or NULL for default
// Returns handle or 0 on error
StreamALSACapture stream_alsa_capture_create(
    const char* device_id,
    int32_t sample_rate,
    int32_t channels,
    StreamALSAAudioCallback callback,
    void* user_data
);

// Start/stop audio capture
int32_t stream_alsa_capture_start(StreamALSACapture handle);
int32_t stream_alsa_capture_stop(StreamALSACapture handle);
void stream_alsa_capture_destroy(StreamALSACapture handle);

// Get actual capture parameters (may differ from requested)
int32_t stream_alsa_capture_get_sample_rate(StreamALSACapture handle);
int32_t stream_alsa_capture_get_channels(StreamALSACapture handle);

// Get last error message
const char* stream_alsa_get_error(void);

#ifdef __cplusplus
}
#endif

#endif // STREAM_ALSA_H
