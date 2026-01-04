// media_av1.h - Thin wrapper for libaom (AV1) with simple API
// This wrapper exposes only primitives, no complex structs.
// Use with purego for Go bindings.

#ifndef MEDIA_AV1_H
#define MEDIA_AV1_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef uint64_t media_av1_encoder_t;
typedef uint64_t media_av1_decoder_t;

// Frame types
#define MEDIA_AV1_FRAME_KEY        0
#define MEDIA_AV1_FRAME_INTER      1
#define MEDIA_AV1_FRAME_INTRA_ONLY 2
#define MEDIA_AV1_FRAME_SWITCH     3

// Usage profiles
#define MEDIA_AV1_USAGE_REALTIME   1
#define MEDIA_AV1_USAGE_GOODQUALITY 0

// Error codes
#define MEDIA_AV1_OK              0
#define MEDIA_AV1_ERROR          -1
#define MEDIA_AV1_ERROR_NOMEM    -2
#define MEDIA_AV1_ERROR_INVALID  -3
#define MEDIA_AV1_ERROR_CODEC    -4

// ============================================================================
// Encoder API
// ============================================================================

// Create encoder
// Returns handle on success, 0 on failure
media_av1_encoder_t media_av1_encoder_create(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int usage,          // MEDIA_AV1_USAGE_REALTIME for WebRTC
    int threads         // 0 = auto
);

// Create encoder with SVC configuration
media_av1_encoder_t media_av1_encoder_create_svc(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int usage,
    int threads,
    int temporal_layers,  // 1-4
    int spatial_layers    // 1-4
);

// Encode I420 frame
// Returns: bytes written to out_data, or negative error code
int media_av1_encoder_encode(
    media_av1_encoder_t encoder,
    const uint8_t* y_plane,
    const uint8_t* u_plane,
    const uint8_t* v_plane,
    int y_stride,
    int uv_stride,
    int force_keyframe,     // 1 to force keyframe
    uint8_t* out_data,      // output buffer
    int out_capacity,       // size of output buffer
    int* out_frame_type,    // receives MEDIA_AV1_FRAME_*
    int64_t* out_pts        // receives presentation timestamp
);

// Encode with SVC info output
int media_av1_encoder_encode_svc(
    media_av1_encoder_t encoder,
    const uint8_t* y_plane,
    const uint8_t* u_plane,
    const uint8_t* v_plane,
    int y_stride,
    int uv_stride,
    int force_keyframe,
    uint8_t* out_data,
    int out_capacity,
    int* out_frame_type,
    int64_t* out_pts,
    int* out_temporal_layer,
    int* out_spatial_layer
);

// Get maximum possible output size for a frame
int media_av1_encoder_max_output_size(media_av1_encoder_t encoder);

// Request next frame be a keyframe
void media_av1_encoder_request_keyframe(media_av1_encoder_t encoder);

// Update bitrate
int media_av1_encoder_set_bitrate(media_av1_encoder_t encoder, int bitrate_kbps);

// Set temporal/spatial layers at runtime
int media_av1_encoder_set_temporal_layers(media_av1_encoder_t encoder, int layers);
int media_av1_encoder_set_spatial_layers(media_av1_encoder_t encoder, int layers);

// Get current SVC configuration
void media_av1_encoder_get_svc_config(
    media_av1_encoder_t encoder,
    int* temporal_layers,
    int* spatial_layers,
    int* svc_enabled
);

// Get stats
void media_av1_encoder_get_stats(
    media_av1_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
);

// Destroy encoder
void media_av1_encoder_destroy(media_av1_encoder_t encoder);

// ============================================================================
// Decoder API
// ============================================================================

// Create decoder
media_av1_decoder_t media_av1_decoder_create(int threads);

// Decode AV1 OBU
// Returns: 1 if frame decoded, 0 if buffering, negative on error
int media_av1_decoder_decode(
    media_av1_decoder_t decoder,
    const uint8_t* data,
    int data_len,
    const uint8_t** out_y,
    const uint8_t** out_u,
    const uint8_t** out_v,
    int* out_y_stride,
    int* out_uv_stride,
    int* out_width,
    int* out_height
);

// Get decoded frame dimensions
void media_av1_decoder_get_dimensions(
    media_av1_decoder_t decoder,
    int* width,
    int* height
);

// Get stats
void media_av1_decoder_get_stats(
    media_av1_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
);

// Reset decoder state
int media_av1_decoder_reset(media_av1_decoder_t decoder);

// Destroy decoder
void media_av1_decoder_destroy(media_av1_decoder_t decoder);

// ============================================================================
// Utility
// ============================================================================

// Get last error message (thread-local)
const char* media_av1_get_error(void);

// Check if codec is available
int media_av1_encoder_available(void);
int media_av1_decoder_available(void);

#ifdef __cplusplus
}
#endif

#endif // MEDIA_AV1_H
