// stream_av1.h - Thin wrapper for libaom (AV1) with simple API
// This wrapper exposes only primitives, no complex structs.
// Use with purego for Go bindings.

#ifndef STREAM_AV1_H
#define STREAM_AV1_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef uint64_t stream_av1_encoder_t;
typedef uint64_t stream_av1_decoder_t;

// Frame types
#define STREAM_AV1_FRAME_KEY        0
#define STREAM_AV1_FRAME_INTER      1
#define STREAM_AV1_FRAME_INTRA_ONLY 2
#define STREAM_AV1_FRAME_SWITCH     3

// Usage profiles
#define STREAM_AV1_USAGE_REALTIME   1
#define STREAM_AV1_USAGE_GOODQUALITY 0

// Error codes
#define STREAM_AV1_OK              0
#define STREAM_AV1_ERROR          -1
#define STREAM_AV1_ERROR_NOMEM    -2
#define STREAM_AV1_ERROR_INVALID  -3
#define STREAM_AV1_ERROR_CODEC    -4

// ============================================================================
// Encoder API
// ============================================================================

// Create encoder
// Returns handle on success, 0 on failure
stream_av1_encoder_t stream_av1_encoder_create(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int usage,          // STREAM_AV1_USAGE_REALTIME for WebRTC
    int threads         // 0 = auto
);

// Create encoder with SVC configuration
stream_av1_encoder_t stream_av1_encoder_create_svc(
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
int stream_av1_encoder_encode(
    stream_av1_encoder_t encoder,
    const uint8_t* y_plane,
    const uint8_t* u_plane,
    const uint8_t* v_plane,
    int y_stride,
    int uv_stride,
    int force_keyframe,     // 1 to force keyframe
    uint8_t* out_data,      // output buffer
    int out_capacity,       // size of output buffer
    int* out_frame_type,    // receives STREAM_AV1_FRAME_*
    int64_t* out_pts        // receives presentation timestamp
);

// Encode with SVC info output
int stream_av1_encoder_encode_svc(
    stream_av1_encoder_t encoder,
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
int stream_av1_encoder_max_output_size(stream_av1_encoder_t encoder);

// Request next frame be a keyframe
void stream_av1_encoder_request_keyframe(stream_av1_encoder_t encoder);

// Update bitrate
int stream_av1_encoder_set_bitrate(stream_av1_encoder_t encoder, int bitrate_kbps);

// Set temporal/spatial layers at runtime
int stream_av1_encoder_set_temporal_layers(stream_av1_encoder_t encoder, int layers);
int stream_av1_encoder_set_spatial_layers(stream_av1_encoder_t encoder, int layers);

// Get current SVC configuration
void stream_av1_encoder_get_svc_config(
    stream_av1_encoder_t encoder,
    int* temporal_layers,
    int* spatial_layers,
    int* svc_enabled
);

// Get stats
void stream_av1_encoder_get_stats(
    stream_av1_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
);

// Destroy encoder
void stream_av1_encoder_destroy(stream_av1_encoder_t encoder);

// ============================================================================
// Decoder API
// ============================================================================

// Create decoder
stream_av1_decoder_t stream_av1_decoder_create(int threads);

// Decode AV1 OBU
// Returns: 1 if frame decoded, 0 if buffering, negative on error
int stream_av1_decoder_decode(
    stream_av1_decoder_t decoder,
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
void stream_av1_decoder_get_dimensions(
    stream_av1_decoder_t decoder,
    int* width,
    int* height
);

// Get stats
void stream_av1_decoder_get_stats(
    stream_av1_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
);

// Reset decoder state
int stream_av1_decoder_reset(stream_av1_decoder_t decoder);

// Destroy decoder
void stream_av1_decoder_destroy(stream_av1_decoder_t decoder);

// ============================================================================
// Utility
// ============================================================================

// Get last error message (thread-local)
const char* stream_av1_get_error(void);

// Check if codec is available
int stream_av1_encoder_available(void);
int stream_av1_decoder_available(void);

#ifdef __cplusplus
}
#endif

#endif // STREAM_AV1_H
