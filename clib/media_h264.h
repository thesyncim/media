// media_h264.h - Thin wrapper for x264 with simple API
// This wrapper exposes only primitives, no complex structs.
// Use with purego for Go bindings.

#ifndef MEDIA_H264_H
#define MEDIA_H264_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef uint64_t media_h264_encoder_t;
typedef uint64_t media_h264_decoder_t;

// Profiles
#define MEDIA_H264_PROFILE_BASELINE     66
#define MEDIA_H264_PROFILE_MAIN         77
#define MEDIA_H264_PROFILE_HIGH         100
#define MEDIA_H264_PROFILE_CONSTRAINED_BASELINE 578  // 66 | (1<<9)

// Frame types
#define MEDIA_H264_FRAME_I      0
#define MEDIA_H264_FRAME_P      1
#define MEDIA_H264_FRAME_B      2
#define MEDIA_H264_FRAME_IDR    3

// NAL unit types
#define MEDIA_H264_NAL_SLICE           1
#define MEDIA_H264_NAL_IDR             5
#define MEDIA_H264_NAL_SEI             6
#define MEDIA_H264_NAL_SPS             7
#define MEDIA_H264_NAL_PPS             8

// Error codes
#define MEDIA_H264_OK              0
#define MEDIA_H264_ERROR          -1
#define MEDIA_H264_ERROR_NOMEM    -2
#define MEDIA_H264_ERROR_INVALID  -3
#define MEDIA_H264_ERROR_CODEC    -4

// ============================================================================
// Encoder API
// ============================================================================

// Create encoder
// Returns handle on success, 0 on failure
media_h264_encoder_t media_h264_encoder_create(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int profile,        // MEDIA_H264_PROFILE_*
    int threads         // 0 = auto
);

// Encode I420 frame
// Returns: bytes written to out_data, or negative error code
int media_h264_encoder_encode(
    media_h264_encoder_t encoder,
    const uint8_t* y_plane,
    const uint8_t* u_plane,
    const uint8_t* v_plane,
    int y_stride,
    int uv_stride,
    int force_keyframe,     // 1 to force IDR
    uint8_t* out_data,      // output buffer
    int out_capacity,       // size of output buffer
    int* out_frame_type,    // receives MEDIA_H264_FRAME_*
    int64_t* out_pts,       // receives presentation timestamp
    int64_t* out_dts        // receives decoding timestamp
);

// Get maximum possible output size for a frame
int media_h264_encoder_max_output_size(media_h264_encoder_t encoder);

// Request next frame be an IDR
void media_h264_encoder_request_keyframe(media_h264_encoder_t encoder);

// Update bitrate
int media_h264_encoder_set_bitrate(media_h264_encoder_t encoder, int bitrate_kbps);

// Get SPS/PPS for out-of-band signaling
// Returns bytes written, or negative on error
int media_h264_encoder_get_sps_pps(
    media_h264_encoder_t encoder,
    uint8_t* sps_out,
    int sps_capacity,
    int* sps_len,
    uint8_t* pps_out,
    int pps_capacity,
    int* pps_len
);

// Get stats
void media_h264_encoder_get_stats(
    media_h264_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
);

// Destroy encoder
void media_h264_encoder_destroy(media_h264_encoder_t encoder);

// ============================================================================
// Decoder API (using FFmpeg/libavcodec or OpenH264)
// ============================================================================

// Create decoder
media_h264_decoder_t media_h264_decoder_create(int threads);

// Decode H.264 NAL units
// Returns: 1 if frame decoded, 0 if buffering, negative on error
int media_h264_decoder_decode(
    media_h264_decoder_t decoder,
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
void media_h264_decoder_get_dimensions(
    media_h264_decoder_t decoder,
    int* width,
    int* height
);

// Get stats
void media_h264_decoder_get_stats(
    media_h264_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
);

// Reset decoder state
int media_h264_decoder_reset(media_h264_decoder_t decoder);

// Destroy decoder
void media_h264_decoder_destroy(media_h264_decoder_t decoder);

// ============================================================================
// Utility
// ============================================================================

// Get last error message (thread-local)
const char* media_h264_get_error(void);

// Check if H.264 encoder/decoder is available
int media_h264_encoder_available(void);
int media_h264_decoder_available(void);

#ifdef __cplusplus
}
#endif

#endif // MEDIA_H264_H
