// stream_opus.c - Thin wrapper for libopus
// Compile: cc -shared -fPIC -O2 -o libstream_opus.so stream_opus.c -lopus
// macOS:   cc -shared -fPIC -O2 -o libstream_opus.dylib stream_opus.c -lopus

#include "stream_opus.h"
#include <opus/opus.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

// Thread-local error message
static _Thread_local char g_error_msg[256];

static void set_error(const char* msg) {
    strncpy(g_error_msg, msg, sizeof(g_error_msg) - 1);
    g_error_msg[sizeof(g_error_msg) - 1] = '\0';
}

static void set_opus_error(int err) {
    snprintf(g_error_msg, sizeof(g_error_msg), "opus error: %s", opus_strerror(err));
}

// ============================================================================
// Encoder Implementation
// ============================================================================

typedef struct {
    OpusEncoder* encoder;
    int32_t sample_rate;
    int32_t channels;
    int32_t application;
    uint64_t frames_encoded;
    uint64_t bytes_encoded;
} encoder_state_t;

stream_opus_encoder_t stream_opus_encoder_create(
    int32_t sample_rate,
    int32_t channels,
    int32_t application
) {
    // Validate parameters
    if (channels != 1 && channels != 2) {
        set_error("channels must be 1 or 2");
        return 0;
    }

    if (sample_rate != 8000 && sample_rate != 12000 &&
        sample_rate != 16000 && sample_rate != 24000 &&
        sample_rate != 48000) {
        set_error("sample_rate must be 8000, 12000, 16000, 24000, or 48000");
        return 0;
    }

    encoder_state_t* enc = (encoder_state_t*)calloc(1, sizeof(encoder_state_t));
    if (!enc) {
        set_error("failed to allocate encoder state");
        return 0;
    }

    int err;
    enc->encoder = opus_encoder_create(sample_rate, channels, application, &err);
    if (err != OPUS_OK || !enc->encoder) {
        set_opus_error(err);
        free(enc);
        return 0;
    }

    enc->sample_rate = sample_rate;
    enc->channels = channels;
    enc->application = application;

    // Set default bitrate based on application
    int32_t default_bitrate = 64000; // 64 kbps default
    if (application == OPUS_APPLICATION_VOIP) {
        default_bitrate = 24000; // 24 kbps for voice
    }
    opus_encoder_ctl(enc->encoder, OPUS_SET_BITRATE(default_bitrate));

    // Enable VBR by default
    opus_encoder_ctl(enc->encoder, OPUS_SET_VBR(1));

    return (stream_opus_encoder_t)(uintptr_t)enc;
}

int32_t stream_opus_encoder_encode(
    stream_opus_encoder_t encoder,
    const int16_t* pcm,
    int32_t frame_size,
    uint8_t* out_data,
    int32_t out_capacity
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int32_t result = opus_encode(enc->encoder, pcm, frame_size, out_data, out_capacity);
    if (result < 0) {
        set_opus_error(result);
        return STREAM_OPUS_ERROR_CODEC;
    }

    enc->frames_encoded++;
    enc->bytes_encoded += result;

    return result;
}

int32_t stream_opus_encoder_encode_float(
    stream_opus_encoder_t encoder,
    const float* pcm,
    int32_t frame_size,
    uint8_t* out_data,
    int32_t out_capacity
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int32_t result = opus_encode_float(enc->encoder, pcm, frame_size, out_data, out_capacity);
    if (result < 0) {
        set_opus_error(result);
        return STREAM_OPUS_ERROR_CODEC;
    }

    enc->frames_encoded++;
    enc->bytes_encoded += result;

    return result;
}

int32_t stream_opus_encoder_set_bitrate(stream_opus_encoder_t encoder, int32_t bitrate) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int err = opus_encoder_ctl(enc->encoder, OPUS_SET_BITRATE(bitrate));
    if (err != OPUS_OK) {
        set_opus_error(err);
        return STREAM_OPUS_ERROR_CODEC;
    }
    return STREAM_OPUS_OK;
}

int32_t stream_opus_encoder_get_bitrate(stream_opus_encoder_t encoder) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        return 0;
    }

    opus_int32 bitrate;
    opus_encoder_ctl(enc->encoder, OPUS_GET_BITRATE(&bitrate));
    return bitrate;
}

int32_t stream_opus_encoder_set_complexity(stream_opus_encoder_t encoder, int32_t complexity) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    if (complexity < 0 || complexity > 10) {
        set_error("complexity must be 0-10");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int err = opus_encoder_ctl(enc->encoder, OPUS_SET_COMPLEXITY(complexity));
    if (err != OPUS_OK) {
        set_opus_error(err);
        return STREAM_OPUS_ERROR_CODEC;
    }
    return STREAM_OPUS_OK;
}

int32_t stream_opus_encoder_set_dtx(stream_opus_encoder_t encoder, int32_t enabled) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int err = opus_encoder_ctl(enc->encoder, OPUS_SET_DTX(enabled ? 1 : 0));
    if (err != OPUS_OK) {
        set_opus_error(err);
        return STREAM_OPUS_ERROR_CODEC;
    }
    return STREAM_OPUS_OK;
}

int32_t stream_opus_encoder_set_fec(stream_opus_encoder_t encoder, int32_t enabled) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int err = opus_encoder_ctl(enc->encoder, OPUS_SET_INBAND_FEC(enabled ? 1 : 0));
    if (err != OPUS_OK) {
        set_opus_error(err);
        return STREAM_OPUS_ERROR_CODEC;
    }
    return STREAM_OPUS_OK;
}

int32_t stream_opus_encoder_set_packet_loss(stream_opus_encoder_t encoder, int32_t percentage) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->encoder) {
        set_error("invalid encoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    if (percentage < 0 || percentage > 100) {
        set_error("percentage must be 0-100");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int err = opus_encoder_ctl(enc->encoder, OPUS_SET_PACKET_LOSS_PERC(percentage));
    if (err != OPUS_OK) {
        set_opus_error(err);
        return STREAM_OPUS_ERROR_CODEC;
    }
    return STREAM_OPUS_OK;
}

void stream_opus_encoder_get_stats(
    stream_opus_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* bytes_encoded
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc) return;
    if (frames_encoded) *frames_encoded = enc->frames_encoded;
    if (bytes_encoded) *bytes_encoded = enc->bytes_encoded;
}

void stream_opus_encoder_destroy(stream_opus_encoder_t encoder) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc) return;

    if (enc->encoder) {
        opus_encoder_destroy(enc->encoder);
    }
    free(enc);
}

// ============================================================================
// Decoder Implementation
// ============================================================================

typedef struct {
    OpusDecoder* decoder;
    int32_t sample_rate;
    int32_t channels;
    uint64_t frames_decoded;
    uint64_t bytes_decoded;
    uint64_t plc_frames;
} decoder_state_t;

stream_opus_decoder_t stream_opus_decoder_create(
    int32_t sample_rate,
    int32_t channels
) {
    // Validate parameters
    if (channels != 1 && channels != 2) {
        set_error("channels must be 1 or 2");
        return 0;
    }

    if (sample_rate != 8000 && sample_rate != 12000 &&
        sample_rate != 16000 && sample_rate != 24000 &&
        sample_rate != 48000) {
        set_error("sample_rate must be 8000, 12000, 16000, 24000, or 48000");
        return 0;
    }

    decoder_state_t* dec = (decoder_state_t*)calloc(1, sizeof(decoder_state_t));
    if (!dec) {
        set_error("failed to allocate decoder state");
        return 0;
    }

    int err;
    dec->decoder = opus_decoder_create(sample_rate, channels, &err);
    if (err != OPUS_OK || !dec->decoder) {
        set_opus_error(err);
        free(dec);
        return 0;
    }

    dec->sample_rate = sample_rate;
    dec->channels = channels;

    return (stream_opus_decoder_t)(uintptr_t)dec;
}

int32_t stream_opus_decoder_decode(
    stream_opus_decoder_t decoder,
    const uint8_t* data,
    int32_t data_len,
    int16_t* pcm,
    int32_t frame_size,
    int32_t decode_fec
) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec || !dec->decoder) {
        set_error("invalid decoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int32_t result = opus_decode(dec->decoder, data, data_len, pcm, frame_size, decode_fec);
    if (result < 0) {
        set_opus_error(result);
        return STREAM_OPUS_ERROR_CODEC;
    }

    dec->frames_decoded++;
    if (data == NULL || data_len == 0) {
        dec->plc_frames++;
    } else {
        dec->bytes_decoded += data_len;
    }

    return result;
}

int32_t stream_opus_decoder_decode_float(
    stream_opus_decoder_t decoder,
    const uint8_t* data,
    int32_t data_len,
    float* pcm,
    int32_t frame_size,
    int32_t decode_fec
) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec || !dec->decoder) {
        set_error("invalid decoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int32_t result = opus_decode_float(dec->decoder, data, data_len, pcm, frame_size, decode_fec);
    if (result < 0) {
        set_opus_error(result);
        return STREAM_OPUS_ERROR_CODEC;
    }

    dec->frames_decoded++;
    if (data == NULL || data_len == 0) {
        dec->plc_frames++;
    } else {
        dec->bytes_decoded += data_len;
    }

    return result;
}

void stream_opus_decoder_get_stats(
    stream_opus_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* bytes_decoded,
    uint64_t* plc_frames
) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec) return;
    if (frames_decoded) *frames_decoded = dec->frames_decoded;
    if (bytes_decoded) *bytes_decoded = dec->bytes_decoded;
    if (plc_frames) *plc_frames = dec->plc_frames;
}

int32_t stream_opus_decoder_reset(stream_opus_decoder_t decoder) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec || !dec->decoder) {
        set_error("invalid decoder handle");
        return STREAM_OPUS_ERROR_INVALID;
    }

    int err = opus_decoder_ctl(dec->decoder, OPUS_RESET_STATE);
    if (err != OPUS_OK) {
        set_opus_error(err);
        return STREAM_OPUS_ERROR_CODEC;
    }
    return STREAM_OPUS_OK;
}

int32_t stream_opus_packet_get_samples(
    const uint8_t* data,
    int32_t data_len,
    int32_t sample_rate
) {
    if (!data || data_len <= 0) {
        return 0;
    }
    return opus_packet_get_nb_samples(data, data_len, sample_rate);
}

void stream_opus_decoder_destroy(stream_opus_decoder_t decoder) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec) return;

    if (dec->decoder) {
        opus_decoder_destroy(dec->decoder);
    }
    free(dec);
}

// ============================================================================
// Utility
// ============================================================================

const char* stream_opus_get_error(void) {
    return g_error_msg;
}

const char* stream_opus_get_version(void) {
    return opus_get_version_string();
}
