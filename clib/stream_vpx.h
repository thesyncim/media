// stream_vpx.h - Thin wrapper for libvpx with simple API
// This wrapper exposes only primitives, no complex structs.
// Use with purego for Go bindings.

#ifndef STREAM_VPX_H
#define STREAM_VPX_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef uint64_t stream_vpx_encoder_t;
typedef uint64_t stream_vpx_decoder_t;

// Codec types
#define STREAM_VPX_CODEC_VP8 0
#define STREAM_VPX_CODEC_VP9 1

// Frame types
#define STREAM_VPX_FRAME_KEY   0
#define STREAM_VPX_FRAME_DELTA 1

// Error codes
#define STREAM_VPX_OK              0
#define STREAM_VPX_ERROR          -1
#define STREAM_VPX_ERROR_NOMEM    -2
#define STREAM_VPX_ERROR_INVALID  -3
#define STREAM_VPX_ERROR_CODEC    -4

// ============================================================================
// Encoder API
// ============================================================================

// Create encoder
// Returns handle on success, 0 on failure
stream_vpx_encoder_t stream_vpx_encoder_create(
    int codec,          // STREAM_VPX_CODEC_VP8 or VP9
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int threads         // 0 = auto
);

// Create encoder with SVC (Scalable Video Coding) configuration
// temporal_layers: 1-4, number of temporal layers (0 = disable SVC)
// spatial_layers: 1-3, number of spatial layers (VP9 only, 0 = disable)
stream_vpx_encoder_t stream_vpx_encoder_create_svc(
    int codec,
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int threads,
    int temporal_layers,
    int spatial_layers
);

// Encode I420 frame
// Returns: bytes written to out_data, or negative error code
// out_data must be pre-allocated (use stream_vpx_encoder_max_output_size)
int stream_vpx_encoder_encode(
    stream_vpx_encoder_t encoder,
    const uint8_t* y_plane,
    const uint8_t* u_plane,
    const uint8_t* v_plane,
    int y_stride,
    int uv_stride,
    int force_keyframe,     // 1 to force keyframe
    uint8_t* out_data,      // output buffer
    int out_capacity,       // size of output buffer
    int* out_frame_type,    // receives STREAM_VPX_FRAME_KEY or DELTA
    int64_t* out_pts        // receives presentation timestamp
);

// Encode I420 frame with SVC info output
int stream_vpx_encoder_encode_svc(
    stream_vpx_encoder_t encoder,
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
    int* out_temporal_layer,  // receives temporal layer id (0-3)
    int* out_spatial_layer    // receives spatial layer id (0-2)
);

// Get maximum possible output size for a frame
int stream_vpx_encoder_max_output_size(stream_vpx_encoder_t encoder);

// Request next frame be a keyframe
void stream_vpx_encoder_request_keyframe(stream_vpx_encoder_t encoder);

// Update bitrate
int stream_vpx_encoder_set_bitrate(stream_vpx_encoder_t encoder, int bitrate_kbps);

// Set temporal/spatial layers at runtime (VP9 only)
// Returns 0 on success, negative on error
int stream_vpx_encoder_set_temporal_layers(stream_vpx_encoder_t encoder, int layers);
int stream_vpx_encoder_set_spatial_layers(stream_vpx_encoder_t encoder, int layers);

// Get current SVC configuration
void stream_vpx_encoder_get_svc_config(
    stream_vpx_encoder_t encoder,
    int* temporal_layers,
    int* spatial_layers,
    int* svc_enabled
);

// Get stats
void stream_vpx_encoder_get_stats(
    stream_vpx_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
);

// Destroy encoder
void stream_vpx_encoder_destroy(stream_vpx_encoder_t encoder);

// ============================================================================
// Decoder API
// ============================================================================

// Create decoder
stream_vpx_decoder_t stream_vpx_decoder_create(
    int codec,          // STREAM_VPX_CODEC_VP8 or VP9
    int threads         // 0 = auto
);

// Decode frame
// Returns: 1 if frame decoded, 0 if buffering, negative on error
// out_* receive decoded frame data (y/u/v planes point into internal buffer)
int stream_vpx_decoder_decode(
    stream_vpx_decoder_t decoder,
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

// Get decoded frame dimensions (after first decode)
void stream_vpx_decoder_get_dimensions(
    stream_vpx_decoder_t decoder,
    int* width,
    int* height
);

// Get stats
void stream_vpx_decoder_get_stats(
    stream_vpx_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
);

// Reset decoder state
int stream_vpx_decoder_reset(stream_vpx_decoder_t decoder);

// Destroy decoder
void stream_vpx_decoder_destroy(stream_vpx_decoder_t decoder);

// ============================================================================
// Utility
// ============================================================================

// Get last error message (thread-local)
const char* stream_vpx_get_error(void);

// Check if codec is available
int stream_vpx_codec_available(int codec);

#ifdef __cplusplus
}
#endif

#endif // STREAM_VPX_H
