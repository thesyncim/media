// stream_av1.c - libaom wrapper implementation
// Thin wrapper around libaom for AV1 encoding/decoding

#include "stream_av1.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdarg.h>

#ifdef __has_include
#if __has_include(<aom/aom_encoder.h>)
#define HAS_AOM_ENCODER 1
#include <aom/aom_encoder.h>
#include <aom/aomcx.h>
#endif
#if __has_include(<aom/aom_decoder.h>)
#define HAS_AOM_DECODER 1
#include <aom/aom_decoder.h>
#include <aom/aomdx.h>
#endif
#endif

#ifndef HAS_AOM_ENCODER
#define HAS_AOM_ENCODER 0
#endif
#ifndef HAS_AOM_DECODER
#define HAS_AOM_DECODER 0
#endif

// Thread-local error message
static _Thread_local char g_error_msg[256] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(g_error_msg, sizeof(g_error_msg), fmt, args);
    va_end(args);
}

const char* stream_av1_get_error(void) {
    return g_error_msg[0] ? g_error_msg : NULL;
}

#if HAS_AOM_ENCODER

// Encoder state
typedef struct {
    aom_codec_ctx_t codec;
    aom_codec_enc_cfg_t cfg;
    aom_image_t* img;

    int width;
    int height;
    int fps;

    int force_keyframe;
    int64_t pts;

    // SVC state
    int svc_enabled;
    int temporal_layers;
    int spatial_layers;

    // Stats
    uint64_t frames_encoded;
    uint64_t keyframes_encoded;
    uint64_t bytes_encoded;

    // Output buffer
    uint8_t* output_buf;
    int output_size;
} encoder_state_t;

int stream_av1_encoder_available(void) {
    return 1;
}

stream_av1_encoder_t stream_av1_encoder_create(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int usage,
    int threads
) {
    return stream_av1_encoder_create_svc(width, height, fps, bitrate_kbps, usage, threads, 1, 1);
}

stream_av1_encoder_t stream_av1_encoder_create_svc(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int usage,
    int threads,
    int temporal_layers,
    int spatial_layers
) {
    encoder_state_t* state = calloc(1, sizeof(encoder_state_t));
    if (!state) {
        set_error("Failed to allocate encoder state");
        return 0;
    }

    state->width = width;
    state->height = height;
    state->fps = fps > 0 ? fps : 30;
    state->temporal_layers = temporal_layers > 0 ? temporal_layers : 1;
    state->spatial_layers = spatial_layers > 0 ? spatial_layers : 1;
    state->svc_enabled = (state->temporal_layers > 1 || state->spatial_layers > 1);

    // Get default config
    aom_codec_iface_t* iface = aom_codec_av1_cx();
    aom_codec_err_t res = aom_codec_enc_config_default(iface, &state->cfg,
        usage == STREAM_AV1_USAGE_REALTIME ? AOM_USAGE_REALTIME : AOM_USAGE_GOOD_QUALITY);
    if (res != AOM_CODEC_OK) {
        set_error("Failed to get default config: %s", aom_codec_err_to_string(res));
        free(state);
        return 0;
    }

    // Configure for RTC
    state->cfg.g_w = width;
    state->cfg.g_h = height;
    state->cfg.g_timebase.num = 1;
    state->cfg.g_timebase.den = state->fps;
    state->cfg.rc_target_bitrate = bitrate_kbps;
    state->cfg.g_threads = threads > 0 ? threads : 4;

    // Low latency settings
    state->cfg.g_lag_in_frames = 0;
    state->cfg.rc_end_usage = AOM_CBR;
    state->cfg.g_error_resilient = AOM_ERROR_RESILIENT_DEFAULT;
    state->cfg.kf_mode = AOM_KF_DISABLED;  // Only keyframes on explicit request (PLI)
    state->cfg.kf_max_dist = 0;

    // SVC configuration
    if (state->svc_enabled) {
        state->cfg.g_pass = AOM_RC_ONE_PASS;
        // SVC setup would go here
    }

    // Initialize encoder
    res = aom_codec_enc_init(&state->codec, iface, &state->cfg, 0);
    if (res != AOM_CODEC_OK) {
        set_error("Failed to init encoder: %s", aom_codec_err_to_string(res));
        free(state);
        return 0;
    }

    // Set realtime options
    if (usage == STREAM_AV1_USAGE_REALTIME) {
        aom_codec_control(&state->codec, AOME_SET_CPUUSED, 8);  // Fastest
        aom_codec_control(&state->codec, AV1E_SET_TILE_COLUMNS, 2);
        aom_codec_control(&state->codec, AV1E_SET_TILE_ROWS, 1);
        aom_codec_control(&state->codec, AV1E_SET_ROW_MT, 1);
    }

    // Allocate image
    state->img = aom_img_alloc(NULL, AOM_IMG_FMT_I420, width, height, 16);
    if (!state->img) {
        set_error("Failed to allocate image");
        aom_codec_destroy(&state->codec);
        free(state);
        return 0;
    }

    // Allocate output buffer
    state->output_buf = malloc(width * height * 3 / 2);
    if (!state->output_buf) {
        aom_img_free(state->img);
        aom_codec_destroy(&state->codec);
        free(state);
        return 0;
    }

    return (stream_av1_encoder_t)(uintptr_t)state;
}

int stream_av1_encoder_encode(
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
    int64_t* out_pts
) {
    int temporal_layer, spatial_layer;
    return stream_av1_encoder_encode_svc(encoder, y_plane, u_plane, v_plane,
        y_stride, uv_stride, force_keyframe, out_data, out_capacity,
        out_frame_type, out_pts, &temporal_layer, &spatial_layer);
}

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
) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) {
        set_error("Invalid encoder handle");
        return STREAM_AV1_ERROR_INVALID;
    }

    // Copy input to image
    for (int y = 0; y < state->height; y++) {
        memcpy(state->img->planes[0] + y * state->img->stride[0],
               y_plane + y * y_stride, state->width);
    }
    for (int y = 0; y < state->height / 2; y++) {
        memcpy(state->img->planes[1] + y * state->img->stride[1],
               u_plane + y * uv_stride, state->width / 2);
        memcpy(state->img->planes[2] + y * state->img->stride[2],
               v_plane + y * uv_stride, state->width / 2);
    }

    aom_enc_frame_flags_t flags = 0;
    if (force_keyframe || state->force_keyframe) {
        flags |= AOM_EFLAG_FORCE_KF;
        state->force_keyframe = 0;
    }

    aom_codec_err_t res = aom_codec_encode(&state->codec, state->img,
                                           state->pts++, 1, flags);
    if (res != AOM_CODEC_OK) {
        set_error("Encode failed: %s", aom_codec_err_to_string(res));
        return STREAM_AV1_ERROR_CODEC;
    }

    // Get encoded data
    aom_codec_iter_t iter = NULL;
    const aom_codec_cx_pkt_t* pkt;
    int total_size = 0;

    while ((pkt = aom_codec_get_cx_data(&state->codec, &iter)) != NULL) {
        if (pkt->kind == AOM_CODEC_CX_FRAME_PKT) {
            if (total_size + (int)pkt->data.frame.sz > out_capacity) {
                set_error("Output buffer too small");
                return STREAM_AV1_ERROR_NOMEM;
            }

            memcpy(out_data + total_size, pkt->data.frame.buf, pkt->data.frame.sz);
            total_size += pkt->data.frame.sz;

            if (out_frame_type) {
                if (pkt->data.frame.flags & AOM_FRAME_IS_KEY) {
                    *out_frame_type = STREAM_AV1_FRAME_KEY;
                    state->keyframes_encoded++;
                } else {
                    *out_frame_type = STREAM_AV1_FRAME_INTER;
                }
            }

            if (out_pts) *out_pts = pkt->data.frame.pts;
        }
    }

    if (out_temporal_layer) *out_temporal_layer = 0;
    if (out_spatial_layer) *out_spatial_layer = 0;

    if (total_size > 0) {
        state->frames_encoded++;
        state->bytes_encoded += total_size;
    }

    return total_size;
}

int stream_av1_encoder_max_output_size(stream_av1_encoder_t encoder) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return 0;
    return state->width * state->height * 3 / 2;
}

void stream_av1_encoder_request_keyframe(stream_av1_encoder_t encoder) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (state) {
        state->force_keyframe = 1;
    }
}

int stream_av1_encoder_set_bitrate(stream_av1_encoder_t encoder, int bitrate_kbps) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return STREAM_AV1_ERROR_INVALID;

    state->cfg.rc_target_bitrate = bitrate_kbps;
    if (aom_codec_enc_config_set(&state->codec, &state->cfg) != AOM_CODEC_OK) {
        set_error("failed to set bitrate: %s", aom_codec_error_detail(&state->codec));
        return STREAM_AV1_ERROR_CODEC;
    }

    return STREAM_AV1_OK;
}

int stream_av1_encoder_set_temporal_layers(stream_av1_encoder_t encoder, int layers) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return STREAM_AV1_ERROR_INVALID;
    state->temporal_layers = layers;
    state->svc_enabled = (layers > 1 || state->spatial_layers > 1);
    return STREAM_AV1_OK;
}

int stream_av1_encoder_set_spatial_layers(stream_av1_encoder_t encoder, int layers) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return STREAM_AV1_ERROR_INVALID;
    state->spatial_layers = layers;
    state->svc_enabled = (state->temporal_layers > 1 || layers > 1);
    return STREAM_AV1_OK;
}

void stream_av1_encoder_get_svc_config(
    stream_av1_encoder_t encoder,
    int* temporal_layers,
    int* spatial_layers,
    int* svc_enabled
) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return;
    if (temporal_layers) *temporal_layers = state->temporal_layers;
    if (spatial_layers) *spatial_layers = state->spatial_layers;
    if (svc_enabled) *svc_enabled = state->svc_enabled;
}

void stream_av1_encoder_get_stats(
    stream_av1_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return;
    if (frames_encoded) *frames_encoded = state->frames_encoded;
    if (keyframes_encoded) *keyframes_encoded = state->keyframes_encoded;
    if (bytes_encoded) *bytes_encoded = state->bytes_encoded;
}

void stream_av1_encoder_destroy(stream_av1_encoder_t encoder) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return;

    if (state->img) aom_img_free(state->img);
    free(state->output_buf);
    aom_codec_destroy(&state->codec);
    free(state);
}

#else // !HAS_AOM_ENCODER

int stream_av1_encoder_available(void) { return 0; }
stream_av1_encoder_t stream_av1_encoder_create(int w, int h, int fps, int br, int usage, int thr) {
    set_error("libaom encoder not available");
    return 0;
}
stream_av1_encoder_t stream_av1_encoder_create_svc(int w, int h, int fps, int br, int usage, int thr, int tl, int sl) {
    set_error("libaom encoder not available");
    return 0;
}
int stream_av1_encoder_encode(stream_av1_encoder_t e, const uint8_t* y, const uint8_t* u, const uint8_t* v,
    int ys, int uvs, int fk, uint8_t* out, int oc, int* ft, int64_t* pts) { return STREAM_AV1_ERROR; }
int stream_av1_encoder_encode_svc(stream_av1_encoder_t e, const uint8_t* y, const uint8_t* u, const uint8_t* v,
    int ys, int uvs, int fk, uint8_t* out, int oc, int* ft, int64_t* pts, int* tl, int* sl) { return STREAM_AV1_ERROR; }
int stream_av1_encoder_max_output_size(stream_av1_encoder_t e) { return 0; }
void stream_av1_encoder_request_keyframe(stream_av1_encoder_t e) {}
int stream_av1_encoder_set_bitrate(stream_av1_encoder_t e, int br) { return STREAM_AV1_ERROR; }
int stream_av1_encoder_set_temporal_layers(stream_av1_encoder_t e, int l) { return STREAM_AV1_ERROR; }
int stream_av1_encoder_set_spatial_layers(stream_av1_encoder_t e, int l) { return STREAM_AV1_ERROR; }
void stream_av1_encoder_get_svc_config(stream_av1_encoder_t e, int* tl, int* sl, int* en) {}
void stream_av1_encoder_get_stats(stream_av1_encoder_t e, uint64_t* f, uint64_t* k, uint64_t* b) {}
void stream_av1_encoder_destroy(stream_av1_encoder_t e) {}

#endif // HAS_AOM_ENCODER

#if HAS_AOM_DECODER

// Decoder state
typedef struct {
    aom_codec_ctx_t codec;
    int width;
    int height;

    uint64_t frames_decoded;
    uint64_t keyframes_decoded;
    uint64_t bytes_decoded;
    uint64_t corrupted_frames;
} decoder_state_t;

int stream_av1_decoder_available(void) { return 1; }

stream_av1_decoder_t stream_av1_decoder_create(int threads) {
    decoder_state_t* state = calloc(1, sizeof(decoder_state_t));
    if (!state) {
        set_error("Failed to allocate decoder state");
        return 0;
    }

    aom_codec_iface_t* iface = aom_codec_av1_dx();
    aom_codec_dec_cfg_t cfg = {0};
    cfg.threads = threads > 0 ? threads : 4;
    cfg.allow_lowbitdepth = 1;

    aom_codec_err_t res = aom_codec_dec_init(&state->codec, iface, &cfg, 0);
    if (res != AOM_CODEC_OK) {
        set_error("Failed to init decoder: %s", aom_codec_err_to_string(res));
        free(state);
        return 0;
    }

    return (stream_av1_decoder_t)(uintptr_t)state;
}

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
) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) {
        set_error("Invalid decoder handle");
        return STREAM_AV1_ERROR_INVALID;
    }

    aom_codec_err_t res = aom_codec_decode(&state->codec, data, data_len, NULL);
    if (res != AOM_CODEC_OK) {
        state->corrupted_frames++;
        set_error("Decode failed: %s", aom_codec_err_to_string(res));
        return STREAM_AV1_ERROR_CODEC;
    }

    aom_codec_iter_t iter = NULL;
    aom_image_t* img = aom_codec_get_frame(&state->codec, &iter);
    if (!img) {
        return 0;  // Buffering
    }

    state->width = img->d_w;
    state->height = img->d_h;

    if (out_y) *out_y = img->planes[0];
    if (out_u) *out_u = img->planes[1];
    if (out_v) *out_v = img->planes[2];
    if (out_y_stride) *out_y_stride = img->stride[0];
    if (out_uv_stride) *out_uv_stride = img->stride[1];
    if (out_width) *out_width = img->d_w;
    if (out_height) *out_height = img->d_h;

    state->frames_decoded++;
    state->bytes_decoded += data_len;

    return 1;
}

void stream_av1_decoder_get_dimensions(stream_av1_decoder_t decoder, int* width, int* height) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) return;
    if (width) *width = state->width;
    if (height) *height = state->height;
}

void stream_av1_decoder_get_stats(
    stream_av1_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) return;
    if (frames_decoded) *frames_decoded = state->frames_decoded;
    if (keyframes_decoded) *keyframes_decoded = state->keyframes_decoded;
    if (bytes_decoded) *bytes_decoded = state->bytes_decoded;
    if (corrupted_frames) *corrupted_frames = state->corrupted_frames;
}

int stream_av1_decoder_reset(stream_av1_decoder_t decoder) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) return STREAM_AV1_ERROR_INVALID;
    // libaom doesn't have explicit reset, we'd need to recreate
    return STREAM_AV1_OK;
}

void stream_av1_decoder_destroy(stream_av1_decoder_t decoder) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) return;
    aom_codec_destroy(&state->codec);
    free(state);
}

#else // !HAS_AOM_DECODER

int stream_av1_decoder_available(void) { return 0; }
stream_av1_decoder_t stream_av1_decoder_create(int threads) {
    set_error("libaom decoder not available");
    return 0;
}
int stream_av1_decoder_decode(stream_av1_decoder_t d, const uint8_t* data, int len,
    const uint8_t** y, const uint8_t** u, const uint8_t** v, int* ys, int* uvs, int* w, int* h) { return STREAM_AV1_ERROR; }
void stream_av1_decoder_get_dimensions(stream_av1_decoder_t d, int* w, int* h) {}
void stream_av1_decoder_get_stats(stream_av1_decoder_t d, uint64_t* f, uint64_t* k, uint64_t* b, uint64_t* c) {}
int stream_av1_decoder_reset(stream_av1_decoder_t d) { return STREAM_AV1_ERROR; }
void stream_av1_decoder_destroy(stream_av1_decoder_t d) {}

#endif // HAS_AOM_DECODER
