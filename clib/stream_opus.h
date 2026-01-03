// stream_opus.h - Thin wrapper for libopus with simple API
// This wrapper exposes only primitives, no complex structs.
// Use with purego for Go bindings.

#ifndef STREAM_OPUS_H
#define STREAM_OPUS_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef uint64_t stream_opus_encoder_t;
typedef uint64_t stream_opus_decoder_t;

// Applications
#define STREAM_OPUS_APPLICATION_VOIP          2048
#define STREAM_OPUS_APPLICATION_AUDIO         2049
#define STREAM_OPUS_APPLICATION_LOWDELAY      2051

// Error codes
#define STREAM_OPUS_OK              0
#define STREAM_OPUS_ERROR          -1
#define STREAM_OPUS_ERROR_NOMEM    -2
#define STREAM_OPUS_ERROR_INVALID  -3
#define STREAM_OPUS_ERROR_CODEC    -4

// ============================================================================
// Encoder API
// ============================================================================

// Create encoder
// Returns handle on success, 0 on failure
// sample_rate: 8000, 12000, 16000, 24000, or 48000
// channels: 1 (mono) or 2 (stereo)
// application: STREAM_OPUS_APPLICATION_VOIP, AUDIO, or LOWDELAY
stream_opus_encoder_t stream_opus_encoder_create(
    int32_t sample_rate,
    int32_t channels,
    int32_t application
);

// Encode PCM samples to Opus
// pcm: interleaved 16-bit PCM samples
// frame_size: number of samples per channel (must be 2.5, 5, 10, 20, 40, 60, 80, 100, or 120 ms worth)
// out_data: output buffer for encoded data
// out_capacity: size of output buffer (recommended: 4000 bytes)
// Returns: bytes written to out_data, or negative error code
int32_t stream_opus_encoder_encode(
    stream_opus_encoder_t encoder,
    const int16_t* pcm,
    int32_t frame_size,
    uint8_t* out_data,
    int32_t out_capacity
);

// Encode float PCM samples to Opus
int32_t stream_opus_encoder_encode_float(
    stream_opus_encoder_t encoder,
    const float* pcm,
    int32_t frame_size,
    uint8_t* out_data,
    int32_t out_capacity
);

// Set encoder bitrate (bits per second)
int32_t stream_opus_encoder_set_bitrate(stream_opus_encoder_t encoder, int32_t bitrate);

// Get encoder bitrate
int32_t stream_opus_encoder_get_bitrate(stream_opus_encoder_t encoder);

// Set complexity (0-10, higher = better quality but slower)
int32_t stream_opus_encoder_set_complexity(stream_opus_encoder_t encoder, int32_t complexity);

// Enable/disable DTX (discontinuous transmission)
int32_t stream_opus_encoder_set_dtx(stream_opus_encoder_t encoder, int32_t enabled);

// Enable/disable FEC (forward error correction)
int32_t stream_opus_encoder_set_fec(stream_opus_encoder_t encoder, int32_t enabled);

// Set packet loss percentage (0-100) for FEC tuning
int32_t stream_opus_encoder_set_packet_loss(stream_opus_encoder_t encoder, int32_t percentage);

// Get encoder stats
void stream_opus_encoder_get_stats(
    stream_opus_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* bytes_encoded
);

// Destroy encoder
void stream_opus_encoder_destroy(stream_opus_encoder_t encoder);

// ============================================================================
// Decoder API
// ============================================================================

// Create decoder
// sample_rate: 8000, 12000, 16000, 24000, or 48000
// channels: 1 (mono) or 2 (stereo)
stream_opus_decoder_t stream_opus_decoder_create(
    int32_t sample_rate,
    int32_t channels
);

// Decode Opus to PCM
// data: encoded Opus data (NULL for PLC - packet loss concealment)
// data_len: length of encoded data (0 for PLC)
// pcm: output buffer for decoded samples
// frame_size: max samples per channel to decode
// decode_fec: 1 to decode FEC data, 0 for normal decoding
// Returns: samples decoded per channel, or negative error code
int32_t stream_opus_decoder_decode(
    stream_opus_decoder_t decoder,
    const uint8_t* data,
    int32_t data_len,
    int16_t* pcm,
    int32_t frame_size,
    int32_t decode_fec
);

// Decode Opus to float PCM
int32_t stream_opus_decoder_decode_float(
    stream_opus_decoder_t decoder,
    const uint8_t* data,
    int32_t data_len,
    float* pcm,
    int32_t frame_size,
    int32_t decode_fec
);

// Get decoder stats
void stream_opus_decoder_get_stats(
    stream_opus_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* bytes_decoded,
    uint64_t* plc_frames
);

// Reset decoder state
int32_t stream_opus_decoder_reset(stream_opus_decoder_t decoder);

// Get number of samples in an Opus packet
int32_t stream_opus_packet_get_samples(
    const uint8_t* data,
    int32_t data_len,
    int32_t sample_rate
);

// Destroy decoder
void stream_opus_decoder_destroy(stream_opus_decoder_t decoder);

// ============================================================================
// Utility
// ============================================================================

// Get last error message (thread-local)
const char* stream_opus_get_error(void);

// Get libopus version string
const char* stream_opus_get_version(void);

#ifdef __cplusplus
}
#endif

#endif // STREAM_OPUS_H
