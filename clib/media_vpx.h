// media_vpx.h - Thin wrapper for libvpx with simple API
// This wrapper exposes only primitives, no complex structs.
// Use with purego for Go bindings.

#ifndef MEDIA_VPX_H
#define MEDIA_VPX_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef uint64_t media_vpx_encoder_t;
typedef uint64_t media_vpx_decoder_t;

// Codec types
#define MEDIA_VPX_CODEC_VP8 0
#define MEDIA_VPX_CODEC_VP9 1

// Frame types
#define MEDIA_VPX_FRAME_KEY   0
#define MEDIA_VPX_FRAME_DELTA 1

// Error codes
#define MEDIA_VPX_OK              0
#define MEDIA_VPX_ERROR          -1
#define MEDIA_VPX_ERROR_NOMEM    -2
#define MEDIA_VPX_ERROR_INVALID  -3
#define MEDIA_VPX_ERROR_CODEC    -4

// ============================================================================
// Encoder API
// ============================================================================

// Create encoder
// Returns handle on success, 0 on failure
media_vpx_encoder_t media_vpx_encoder_create(
    int codec,          // MEDIA_VPX_CODEC_VP8 or VP9
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int threads         // 0 = auto
);

// Create encoder with SVC (Scalable Video Coding) configuration
// temporal_layers: 1-4, number of temporal layers (0 = disable SVC)
// spatial_layers: 1-3, number of spatial layers (VP9 only, 0 = disable)
media_vpx_encoder_t media_vpx_encoder_create_svc(
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
// out_data must be pre-allocated (use media_vpx_encoder_max_output_size)
int media_vpx_encoder_encode(
    media_vpx_encoder_t encoder,
    const uint8_t* y_plane,
    const uint8_t* u_plane,
    const uint8_t* v_plane,
    int y_stride,
    int uv_stride,
    int force_keyframe,     // 1 to force keyframe
    uint8_t* out_data,      // output buffer
    int out_capacity,       // size of output buffer
    int* out_frame_type,    // receives MEDIA_VPX_FRAME_KEY or DELTA
    int64_t* out_pts        // receives presentation timestamp
);

// Encode I420 frame with SVC info output
int media_vpx_encoder_encode_svc(
    media_vpx_encoder_t encoder,
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
int media_vpx_encoder_max_output_size(media_vpx_encoder_t encoder);

// Request next frame be a keyframe
void media_vpx_encoder_request_keyframe(media_vpx_encoder_t encoder);

// Update bitrate
int media_vpx_encoder_set_bitrate(media_vpx_encoder_t encoder, int bitrate_kbps);

// Set temporal/spatial layers at runtime (VP9 only)
// Returns 0 on success, negative on error
int media_vpx_encoder_set_temporal_layers(media_vpx_encoder_t encoder, int layers);
int media_vpx_encoder_set_spatial_layers(media_vpx_encoder_t encoder, int layers);

// Get current SVC configuration
void media_vpx_encoder_get_svc_config(
    media_vpx_encoder_t encoder,
    int* temporal_layers,
    int* spatial_layers,
    int* svc_enabled
);

// Get stats
void media_vpx_encoder_get_stats(
    media_vpx_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
);

// Destroy encoder
void media_vpx_encoder_destroy(media_vpx_encoder_t encoder);

// ============================================================================
// Decoder API
// ============================================================================

// Create decoder
media_vpx_decoder_t media_vpx_decoder_create(
    int codec,          // MEDIA_VPX_CODEC_VP8 or VP9
    int threads         // 0 = auto
);

// Decode frame
// Returns: 1 if frame decoded, 0 if buffering, negative on error
// out_* receive decoded frame data (y/u/v planes point into internal buffer)
int media_vpx_decoder_decode(
    media_vpx_decoder_t decoder,
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
void media_vpx_decoder_get_dimensions(
    media_vpx_decoder_t decoder,
    int* width,
    int* height
);

// Get stats
void media_vpx_decoder_get_stats(
    media_vpx_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
);

// Reset decoder state
int media_vpx_decoder_reset(media_vpx_decoder_t decoder);

// Destroy decoder
void media_vpx_decoder_destroy(media_vpx_decoder_t decoder);

// ============================================================================
// purego-friendly decode API (avoids pointer-to-pointer output parameters)
// ============================================================================

// Decode result structure - all output values in a single contiguous struct
// This avoids purego issues with pointer-to-pointer parameters on arm64
typedef struct {
    uint64_t y_ptr;      // Pointer to Y plane (cast from uint8_t*)
    uint64_t u_ptr;      // Pointer to U plane (cast from uint8_t*)
    uint64_t v_ptr;      // Pointer to V plane (cast from uint8_t*)
    int32_t  y_stride;   // Y plane stride
    int32_t  uv_stride;  // UV plane stride
    int32_t  width;      // Frame width
    int32_t  height;     // Frame height
    int32_t  result;     // 1=decoded, 0=buffering, <0=error
    int32_t  reserved;   // Padding for alignment
} media_vpx_decode_result_t;

// Decode frame with struct output (purego-friendly)
// result_out must point to a valid media_vpx_decode_result_t struct
// Returns: 1 if frame decoded, 0 if buffering, negative on error
int media_vpx_decoder_decode_v2(
    media_vpx_decoder_t decoder,
    const uint8_t* data,
    int data_len,
    media_vpx_decode_result_t* result_out
);

// ============================================================================
// Utility
// ============================================================================

// Get last error message (thread-local)
const char* media_vpx_get_error(void);

// Check if codec is available
int media_vpx_codec_available(int codec);

#ifdef __cplusplus
}
#endif

#endif // MEDIA_VPX_H
