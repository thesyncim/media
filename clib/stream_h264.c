// stream_h264.c - x264 wrapper implementation
// Thin wrapper around x264 for H.264 encoding

#include "stream_h264.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

#ifdef __has_include
#if __has_include(<x264.h>)
#define HAS_X264 1
#include <x264.h>
#endif
#endif

#ifndef HAS_X264
#define HAS_X264 0
#endif

// Thread-local error message
static _Thread_local char g_error_msg[256] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(g_error_msg, sizeof(g_error_msg), fmt, args);
    va_end(args);
}

const char* stream_h264_get_error(void) {
    return g_error_msg[0] ? g_error_msg : NULL;
}

#if HAS_X264

// Encoder state
typedef struct {
    x264_t* encoder;
    x264_param_t param;
    x264_picture_t pic_in;
    x264_picture_t pic_out;

    int width;
    int height;
    int fps;

    int force_idr;
    int64_t pts;

    // Stats
    uint64_t frames_encoded;
    uint64_t keyframes_encoded;
    uint64_t bytes_encoded;

    // SPS/PPS cache
    uint8_t* sps;
    int sps_len;
    uint8_t* pps;
    int pps_len;
} encoder_state_t;

int stream_h264_encoder_available(void) {
    return 1;
}

stream_h264_encoder_t stream_h264_encoder_create(
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int profile,
    int threads
) {
    encoder_state_t* state = calloc(1, sizeof(encoder_state_t));
    if (!state) {
        set_error("Failed to allocate encoder state");
        return 0;
    }

    state->width = width;
    state->height = height;
    state->fps = fps > 0 ? fps : 30;

    // Initialize x264 parameters
    if (x264_param_default_preset(&state->param, "veryfast", "zerolatency") < 0) {
        set_error("Failed to set x264 preset");
        free(state);
        return 0;
    }

    // Set profile
    const char* profile_name = "baseline";
    if (profile == STREAM_H264_PROFILE_MAIN) {
        profile_name = "main";
    } else if (profile == STREAM_H264_PROFILE_HIGH) {
        profile_name = "high";
    }

    if (x264_param_apply_profile(&state->param, profile_name) < 0) {
        set_error("Failed to apply x264 profile");
        free(state);
        return 0;
    }

    // Configure for RTC
    state->param.i_width = width;
    state->param.i_height = height;
    state->param.i_fps_num = state->fps;
    state->param.i_fps_den = 1;
    state->param.i_csp = X264_CSP_I420;

    // Bitrate control
    state->param.rc.i_rc_method = X264_RC_ABR;
    state->param.rc.i_bitrate = bitrate_kbps;
    state->param.rc.i_vbv_max_bitrate = bitrate_kbps * 2;
    state->param.rc.i_vbv_buffer_size = bitrate_kbps;

    // Threading
    state->param.i_threads = threads > 0 ? threads : X264_THREADS_AUTO;
    state->param.i_lookahead_threads = 1;
    state->param.b_sliced_threads = 1;

    // Keyframe interval - disabled, only on explicit request (PLI)
    state->param.i_keyint_max = INT32_MAX;  // Effectively infinite
    state->param.i_keyint_min = INT32_MAX;

    // Low latency settings
    state->param.i_bframe = 0;
    state->param.b_open_gop = 0;
    state->param.i_scenecut_threshold = 0;
    state->param.rc.i_lookahead = 0;
    state->param.i_sync_lookahead = 0;
    state->param.b_vfr_input = 0;

    // Annex B output for RTP
    state->param.b_annexb = 1;
    state->param.b_repeat_headers = 1;  // Repeat SPS/PPS with every IDR (required for WebRTC)

    // Create encoder
    state->encoder = x264_encoder_open(&state->param);
    if (!state->encoder) {
        set_error("Failed to open x264 encoder");
        free(state);
        return 0;
    }

    // Allocate picture
    if (x264_picture_alloc(&state->pic_in, X264_CSP_I420, width, height) < 0) {
        set_error("Failed to allocate x264 picture");
        x264_encoder_close(state->encoder);
        free(state);
        return 0;
    }

    // Extract SPS/PPS
    x264_nal_t* nals;
    int num_nals;
    if (x264_encoder_headers(state->encoder, &nals, &num_nals) >= 0) {
        for (int i = 0; i < num_nals; i++) {
            if (nals[i].i_type == NAL_SPS) {
                state->sps = malloc(nals[i].i_payload);
                memcpy(state->sps, nals[i].p_payload, nals[i].i_payload);
                state->sps_len = nals[i].i_payload;
            } else if (nals[i].i_type == NAL_PPS) {
                state->pps = malloc(nals[i].i_payload);
                memcpy(state->pps, nals[i].p_payload, nals[i].i_payload);
                state->pps_len = nals[i].i_payload;
            }
        }
    }

    return (stream_h264_encoder_t)(uintptr_t)state;
}

int stream_h264_encoder_encode(
    stream_h264_encoder_t encoder,
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
    int64_t* out_dts
) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state || !state->encoder) {
        set_error("Invalid encoder handle");
        return STREAM_H264_ERROR_INVALID;
    }

    // Copy input planes
    for (int y = 0; y < state->height; y++) {
        memcpy(state->pic_in.img.plane[0] + y * state->pic_in.img.i_stride[0],
               y_plane + y * y_stride, state->width);
    }
    for (int y = 0; y < state->height / 2; y++) {
        memcpy(state->pic_in.img.plane[1] + y * state->pic_in.img.i_stride[1],
               u_plane + y * uv_stride, state->width / 2);
        memcpy(state->pic_in.img.plane[2] + y * state->pic_in.img.i_stride[2],
               v_plane + y * uv_stride, state->width / 2);
    }

    state->pic_in.i_pts = state->pts++;

    if (force_keyframe || state->force_idr) {
        state->pic_in.i_type = X264_TYPE_IDR;
        state->force_idr = 0;
    } else {
        state->pic_in.i_type = X264_TYPE_AUTO;
    }

    x264_nal_t* nals;
    int num_nals;
    int frame_size = x264_encoder_encode(state->encoder, &nals, &num_nals,
                                         &state->pic_in, &state->pic_out);

    if (frame_size < 0) {
        set_error("x264 encode failed");
        return STREAM_H264_ERROR_CODEC;
    }

    if (frame_size == 0) {
        return 0;  // No output yet (buffering)
    }

    // Copy NALs to output
    int total_size = 0;
    for (int i = 0; i < num_nals; i++) {
        if (total_size + nals[i].i_payload > out_capacity) {
            set_error("Output buffer too small");
            return STREAM_H264_ERROR_NOMEM;
        }
        memcpy(out_data + total_size, nals[i].p_payload, nals[i].i_payload);
        total_size += nals[i].i_payload;
    }

    // Determine frame type
    if (out_frame_type) {
        if (state->pic_out.b_keyframe) {
            *out_frame_type = STREAM_H264_FRAME_IDR;
        } else if (state->pic_out.i_type == X264_TYPE_P) {
            *out_frame_type = STREAM_H264_FRAME_P;
        } else if (state->pic_out.i_type == X264_TYPE_B) {
            *out_frame_type = STREAM_H264_FRAME_B;
        } else {
            *out_frame_type = STREAM_H264_FRAME_I;
        }
    }

    if (out_pts) *out_pts = state->pic_out.i_pts;
    if (out_dts) *out_dts = state->pic_out.i_dts;

    // Update stats
    state->frames_encoded++;
    state->bytes_encoded += total_size;
    if (state->pic_out.b_keyframe) {
        state->keyframes_encoded++;
    }

    return total_size;
}

int stream_h264_encoder_max_output_size(stream_h264_encoder_t encoder) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return 0;
    // Worst case: raw I420 frame
    return state->width * state->height * 3 / 2;
}

void stream_h264_encoder_request_keyframe(stream_h264_encoder_t encoder) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (state) {
        state->force_idr = 1;
    }
}

int stream_h264_encoder_set_bitrate(stream_h264_encoder_t encoder, int bitrate_kbps) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state || !state->encoder) {
        return STREAM_H264_ERROR_INVALID;
    }

    x264_param_t param;
    x264_encoder_parameters(state->encoder, &param);
    param.rc.i_bitrate = bitrate_kbps;
    param.rc.i_vbv_max_bitrate = bitrate_kbps * 2;

    if (x264_encoder_reconfig(state->encoder, &param) < 0) {
        set_error("Failed to reconfigure bitrate");
        return STREAM_H264_ERROR_CODEC;
    }

    return STREAM_H264_OK;
}

int stream_h264_encoder_get_sps_pps(
    stream_h264_encoder_t encoder,
    uint8_t* sps_out,
    int sps_capacity,
    int* sps_len,
    uint8_t* pps_out,
    int pps_capacity,
    int* pps_len
) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) {
        return STREAM_H264_ERROR_INVALID;
    }

    if (state->sps && sps_out && state->sps_len <= sps_capacity) {
        memcpy(sps_out, state->sps, state->sps_len);
        if (sps_len) *sps_len = state->sps_len;
    }

    if (state->pps && pps_out && state->pps_len <= pps_capacity) {
        memcpy(pps_out, state->pps, state->pps_len);
        if (pps_len) *pps_len = state->pps_len;
    }

    return STREAM_H264_OK;
}

void stream_h264_encoder_get_stats(
    stream_h264_encoder_t encoder,
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

void stream_h264_encoder_destroy(stream_h264_encoder_t encoder) {
    encoder_state_t* state = (encoder_state_t*)(uintptr_t)encoder;
    if (!state) return;

    if (state->encoder) {
        x264_picture_clean(&state->pic_in);
        x264_encoder_close(state->encoder);
    }

    free(state->sps);
    free(state->pps);
    free(state);
}

#else // !HAS_X264

int stream_h264_encoder_available(void) { return 0; }
stream_h264_encoder_t stream_h264_encoder_create(int w, int h, int fps, int br, int prof, int thr) {
    set_error("x264 not available");
    return 0;
}
int stream_h264_encoder_encode(stream_h264_encoder_t e, const uint8_t* y, const uint8_t* u, const uint8_t* v,
    int ys, int uvs, int fk, uint8_t* out, int oc, int* ft, int64_t* pts, int64_t* dts) { return STREAM_H264_ERROR; }
int stream_h264_encoder_max_output_size(stream_h264_encoder_t e) { return 0; }
void stream_h264_encoder_request_keyframe(stream_h264_encoder_t e) {}
int stream_h264_encoder_set_bitrate(stream_h264_encoder_t e, int br) { return STREAM_H264_ERROR; }
int stream_h264_encoder_get_sps_pps(stream_h264_encoder_t e, uint8_t* s, int sc, int* sl, uint8_t* p, int pc, int* pl) { return STREAM_H264_ERROR; }
void stream_h264_encoder_get_stats(stream_h264_encoder_t e, uint64_t* f, uint64_t* k, uint64_t* b) {}
void stream_h264_encoder_destroy(stream_h264_encoder_t e) {}

#endif // HAS_X264

// Decoder stubs - implement with OpenH264 or FFmpeg
int stream_h264_decoder_available(void) { return 0; }
stream_h264_decoder_t stream_h264_decoder_create(int threads) {
    set_error("H.264 decoder not implemented");
    return 0;
}
int stream_h264_decoder_decode(stream_h264_decoder_t d, const uint8_t* data, int len,
    const uint8_t** y, const uint8_t** u, const uint8_t** v, int* ys, int* uvs, int* w, int* h) { return STREAM_H264_ERROR; }
void stream_h264_decoder_get_dimensions(stream_h264_decoder_t d, int* w, int* h) {}
void stream_h264_decoder_get_stats(stream_h264_decoder_t d, uint64_t* f, uint64_t* k, uint64_t* b, uint64_t* c) {}
int stream_h264_decoder_reset(stream_h264_decoder_t d) { return STREAM_H264_ERROR; }
void stream_h264_decoder_destroy(stream_h264_decoder_t d) {}
