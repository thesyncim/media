// stream_vpx.c - Thin wrapper for libvpx
// Compile: cc -shared -fPIC -O2 -o libstream_vpx.so stream_vpx.c -lvpx
// macOS:   cc -shared -fPIC -O2 -o libstream_vpx.dylib stream_vpx.c -lvpx

#include "stream_vpx.h"
#include <vpx/vpx_encoder.h>
#include <vpx/vpx_decoder.h>
#include <vpx/vp8cx.h>
#include <vpx/vp8dx.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

// Thread-local error message
static _Thread_local char g_error_msg[256];

static void set_error(const char* msg) {
    strncpy(g_error_msg, msg, sizeof(g_error_msg) - 1);
    g_error_msg[sizeof(g_error_msg) - 1] = '\0';
}

static void set_vpx_error(vpx_codec_ctx_t* ctx, const char* prefix) {
    const char* err = vpx_codec_error(ctx);
    const char* detail = vpx_codec_error_detail(ctx);
    if (detail) {
        snprintf(g_error_msg, sizeof(g_error_msg), "%s: %s (%s)", prefix, err, detail);
    } else {
        snprintf(g_error_msg, sizeof(g_error_msg), "%s: %s", prefix, err);
    }
}

// ============================================================================
// Encoder Implementation
// ============================================================================

typedef struct {
    vpx_codec_ctx_t codec;
    vpx_image_t raw;
    vpx_codec_enc_cfg_t cfg;
    int codec_type;
    int width;
    int height;
    int initialized;
    int keyframe_requested;
    uint64_t frame_count;
    uint64_t frames_encoded;
    uint64_t keyframes_encoded;
    uint64_t bytes_encoded;
    // SVC configuration
    int temporal_layers;
    int spatial_layers;
    int svc_enabled;
    int current_temporal_layer;
    int current_spatial_layer;
} encoder_state_t;

stream_vpx_encoder_t stream_vpx_encoder_create(
    int codec,
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int threads
) {
    encoder_state_t* enc = (encoder_state_t*)calloc(1, sizeof(encoder_state_t));
    if (!enc) {
        set_error("failed to allocate encoder state");
        return 0;
    }

    // Get codec interface
    vpx_codec_iface_t* iface = NULL;
    switch (codec) {
        case STREAM_VPX_CODEC_VP8:
            iface = vpx_codec_vp8_cx();
            break;
        case STREAM_VPX_CODEC_VP9:
            iface = vpx_codec_vp9_cx();
            break;
        default:
            set_error("invalid codec type");
            free(enc);
            return 0;
    }

    // Get default config
    if (vpx_codec_enc_config_default(iface, &enc->cfg, 0) != VPX_CODEC_OK) {
        set_error("failed to get default config");
        free(enc);
        return 0;
    }

    // Configure encoder
    enc->cfg.g_w = width;
    enc->cfg.g_h = height;
    enc->cfg.g_timebase.num = 1;
    enc->cfg.g_timebase.den = fps;
    enc->cfg.rc_target_bitrate = bitrate_kbps;
    enc->cfg.g_error_resilient = VPX_ERROR_RESILIENT_DEFAULT;
    enc->cfg.g_threads = threads > 0 ? threads : 4;
    enc->cfg.kf_mode = VPX_KF_DISABLED;  // Only keyframes on explicit request (PLI)
    enc->cfg.kf_max_dist = 0;
    enc->cfg.g_lag_in_frames = 0;    // No lag for real-time
    enc->cfg.rc_end_usage = VPX_CBR; // CBR for real-time

    // Initialize codec
    if (vpx_codec_enc_init(&enc->codec, iface, &enc->cfg, 0) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "failed to init encoder");
        free(enc);
        return 0;
    }

    // Set CPU usage (realtime)
    vpx_codec_control(&enc->codec, VP8E_SET_CPUUSED, 6);

    // VP9-specific settings
    if (codec == STREAM_VPX_CODEC_VP9) {
        vpx_codec_control(&enc->codec, VP9E_SET_ROW_MT, 1);
        int tile_columns = 0;
        if (width >= 1280) tile_columns = 2;
        else if (width >= 640) tile_columns = 1;
        vpx_codec_control(&enc->codec, VP9E_SET_TILE_COLUMNS, tile_columns);
    }

    // Allocate image
    if (!vpx_img_alloc(&enc->raw, VPX_IMG_FMT_I420, width, height, 1)) {
        set_error("failed to allocate image buffer");
        vpx_codec_destroy(&enc->codec);
        free(enc);
        return 0;
    }

    enc->codec_type = codec;
    enc->width = width;
    enc->height = height;
    enc->initialized = 1;
    enc->keyframe_requested = 1;  // First frame is keyframe
    enc->temporal_layers = 1;
    enc->spatial_layers = 1;
    enc->svc_enabled = 0;

    return (stream_vpx_encoder_t)(uintptr_t)enc;
}

stream_vpx_encoder_t stream_vpx_encoder_create_svc(
    int codec,
    int width,
    int height,
    int fps,
    int bitrate_kbps,
    int threads,
    int temporal_layers,
    int spatial_layers
) {
    if (codec != STREAM_VPX_CODEC_VP9) {
        // Only VP9 supports full SVC, VP8 has limited temporal layer support
        if (codec == STREAM_VPX_CODEC_VP8 && temporal_layers > 1) {
            set_error("VP8 only supports limited temporal layers");
        }
    }

    encoder_state_t* enc = (encoder_state_t*)calloc(1, sizeof(encoder_state_t));
    if (!enc) {
        set_error("failed to allocate encoder state");
        return 0;
    }

    vpx_codec_iface_t* iface = NULL;
    switch (codec) {
        case STREAM_VPX_CODEC_VP8:
            iface = vpx_codec_vp8_cx();
            break;
        case STREAM_VPX_CODEC_VP9:
            iface = vpx_codec_vp9_cx();
            break;
        default:
            set_error("invalid codec type");
            free(enc);
            return 0;
    }

    if (vpx_codec_enc_config_default(iface, &enc->cfg, 0) != VPX_CODEC_OK) {
        set_error("failed to get default config");
        free(enc);
        return 0;
    }

    enc->cfg.g_w = width;
    enc->cfg.g_h = height;
    enc->cfg.g_timebase.num = 1;
    enc->cfg.g_timebase.den = fps;
    enc->cfg.rc_target_bitrate = bitrate_kbps;
    enc->cfg.g_error_resilient = VPX_ERROR_RESILIENT_DEFAULT;
    enc->cfg.g_threads = threads > 0 ? threads : 4;
    enc->cfg.kf_mode = VPX_KF_AUTO;
    enc->cfg.kf_max_dist = fps * 2;
    enc->cfg.g_lag_in_frames = 0;    // No lag for real-time
    enc->cfg.rc_end_usage = VPX_CBR; // CBR for real-time

    // Configure temporal layers
    if (temporal_layers > 1 && temporal_layers <= 4) {
        enc->cfg.ts_number_layers = temporal_layers;
        // Set layer rates - example for 3 layers: 25%, 50%, 100%
        switch (temporal_layers) {
            case 2:
                enc->cfg.ts_target_bitrate[0] = bitrate_kbps / 2;
                enc->cfg.ts_target_bitrate[1] = bitrate_kbps;
                enc->cfg.ts_rate_decimator[0] = 2;
                enc->cfg.ts_rate_decimator[1] = 1;
                break;
            case 3:
                enc->cfg.ts_target_bitrate[0] = bitrate_kbps / 4;
                enc->cfg.ts_target_bitrate[1] = bitrate_kbps / 2;
                enc->cfg.ts_target_bitrate[2] = bitrate_kbps;
                enc->cfg.ts_rate_decimator[0] = 4;
                enc->cfg.ts_rate_decimator[1] = 2;
                enc->cfg.ts_rate_decimator[2] = 1;
                break;
            case 4:
                enc->cfg.ts_target_bitrate[0] = bitrate_kbps / 8;
                enc->cfg.ts_target_bitrate[1] = bitrate_kbps / 4;
                enc->cfg.ts_target_bitrate[2] = bitrate_kbps / 2;
                enc->cfg.ts_target_bitrate[3] = bitrate_kbps;
                enc->cfg.ts_rate_decimator[0] = 8;
                enc->cfg.ts_rate_decimator[1] = 4;
                enc->cfg.ts_rate_decimator[2] = 2;
                enc->cfg.ts_rate_decimator[3] = 1;
                break;
        }
        enc->cfg.ts_periodicity = temporal_layers;
        for (int i = 0; i < temporal_layers; i++) {
            enc->cfg.ts_layer_id[i] = i;
        }
        enc->temporal_layers = temporal_layers;
        enc->svc_enabled = 1;
    } else {
        enc->temporal_layers = 1;
    }

    // Configure spatial layers (VP9 only)
    if (codec == STREAM_VPX_CODEC_VP9 && spatial_layers > 1 && spatial_layers <= 3) {
        enc->cfg.ss_number_layers = spatial_layers;
        // Set spatial layer scaling - example: 0.5x, 0.75x, 1.0x
        switch (spatial_layers) {
            case 2:
                enc->cfg.ss_target_bitrate[0] = bitrate_kbps / 4;
                enc->cfg.ss_target_bitrate[1] = bitrate_kbps;
                break;
            case 3:
                enc->cfg.ss_target_bitrate[0] = bitrate_kbps / 8;
                enc->cfg.ss_target_bitrate[1] = bitrate_kbps / 2;
                enc->cfg.ss_target_bitrate[2] = bitrate_kbps;
                break;
        }
        enc->spatial_layers = spatial_layers;
        enc->svc_enabled = 1;
    } else {
        enc->spatial_layers = 1;
    }

    if (vpx_codec_enc_init(&enc->codec, iface, &enc->cfg, 0) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "failed to init encoder");
        free(enc);
        return 0;
    }

    vpx_codec_control(&enc->codec, VP8E_SET_CPUUSED, 6);

    if (codec == STREAM_VPX_CODEC_VP9) {
        vpx_codec_control(&enc->codec, VP9E_SET_ROW_MT, 1);
        int tile_columns = 0;
        if (width >= 1280) tile_columns = 2;
        else if (width >= 640) tile_columns = 1;
        vpx_codec_control(&enc->codec, VP9E_SET_TILE_COLUMNS, tile_columns);

        // Enable SVC mode if layers configured
        if (enc->svc_enabled) {
            vpx_codec_control(&enc->codec, VP9E_SET_SVC, 1);
        }
    }

    if (!vpx_img_alloc(&enc->raw, VPX_IMG_FMT_I420, width, height, 1)) {
        set_error("failed to allocate image buffer");
        vpx_codec_destroy(&enc->codec);
        free(enc);
        return 0;
    }

    enc->codec_type = codec;
    enc->width = width;
    enc->height = height;
    enc->initialized = 1;
    enc->keyframe_requested = 1;

    return (stream_vpx_encoder_t)(uintptr_t)enc;
}

int stream_vpx_encoder_encode(
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
    int64_t* out_pts
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->initialized) {
        set_error("invalid encoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    // Copy input planes to vpx image
    int w = enc->width;
    int h = enc->height;
    int uv_w = w / 2;
    int uv_h = h / 2;

    // Copy Y plane
    for (int row = 0; row < h; row++) {
        memcpy(enc->raw.planes[0] + row * enc->raw.stride[0],
               y_plane + row * y_stride, w);
    }
    // Copy U plane
    for (int row = 0; row < uv_h; row++) {
        memcpy(enc->raw.planes[1] + row * enc->raw.stride[1],
               u_plane + row * uv_stride, uv_w);
    }
    // Copy V plane
    for (int row = 0; row < uv_h; row++) {
        memcpy(enc->raw.planes[2] + row * enc->raw.stride[2],
               v_plane + row * uv_stride, uv_w);
    }

    // Determine flags
    vpx_enc_frame_flags_t flags = 0;
    if (force_keyframe || enc->keyframe_requested) {
        flags = VPX_EFLAG_FORCE_KF;
        enc->keyframe_requested = 0;
    }

    vpx_codec_pts_t pts = enc->frame_count++;

    // Encode
    if (vpx_codec_encode(&enc->codec, &enc->raw, pts, 1, flags, VPX_DL_REALTIME) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "encode failed");
        return STREAM_VPX_ERROR_CODEC;
    }

    // Collect output
    int total_size = 0;
    int is_keyframe = 0;

    const vpx_codec_cx_pkt_t* pkt;
    vpx_codec_iter_t iter = NULL;
    while ((pkt = vpx_codec_get_cx_data(&enc->codec, &iter)) != NULL) {
        if (pkt->kind == VPX_CODEC_CX_FRAME_PKT) {
            if (total_size + (int)pkt->data.frame.sz > out_capacity) {
                set_error("output buffer too small");
                return STREAM_VPX_ERROR_NOMEM;
            }
            memcpy(out_data + total_size, pkt->data.frame.buf, pkt->data.frame.sz);
            total_size += pkt->data.frame.sz;

            if (pkt->data.frame.flags & VPX_FRAME_IS_KEY) {
                is_keyframe = 1;
            }
        }
    }

    if (total_size > 0) {
        enc->frames_encoded++;
        if (is_keyframe) enc->keyframes_encoded++;
        enc->bytes_encoded += total_size;
    }

    if (out_frame_type) *out_frame_type = is_keyframe ? STREAM_VPX_FRAME_KEY : STREAM_VPX_FRAME_DELTA;
    if (out_pts) *out_pts = pts;

    return total_size;
}

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
    int* out_temporal_layer,
    int* out_spatial_layer
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->initialized) {
        set_error("invalid encoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    // Copy input planes
    int w = enc->width;
    int h = enc->height;
    int uv_w = w / 2;
    int uv_h = h / 2;

    for (int row = 0; row < h; row++) {
        memcpy(enc->raw.planes[0] + row * enc->raw.stride[0],
               y_plane + row * y_stride, w);
    }
    for (int row = 0; row < uv_h; row++) {
        memcpy(enc->raw.planes[1] + row * enc->raw.stride[1],
               u_plane + row * uv_stride, uv_w);
    }
    for (int row = 0; row < uv_h; row++) {
        memcpy(enc->raw.planes[2] + row * enc->raw.stride[2],
               v_plane + row * uv_stride, uv_w);
    }

    vpx_enc_frame_flags_t flags = 0;
    if (force_keyframe || enc->keyframe_requested) {
        flags = VPX_EFLAG_FORCE_KF;
        enc->keyframe_requested = 0;
    }

    vpx_codec_pts_t pts = enc->frame_count++;

    // For SVC, determine which layer this frame belongs to
    int temporal_layer = 0;
    if (enc->svc_enabled && enc->temporal_layers > 1) {
        // Simple temporal layer assignment based on frame count
        // For 2 layers: 0,1,0,1... (base, enhancement)
        // For 3 layers: 0,2,1,2,0,2,1,2... pattern
        unsigned int frame_idx = enc->frame_count - 1;
        switch (enc->temporal_layers) {
            case 2:
                temporal_layer = frame_idx % 2;
                break;
            case 3:
                // Pattern: 0,2,1,2,0,2,1,2...
                temporal_layer = enc->cfg.ts_layer_id[frame_idx % enc->temporal_layers];
                break;
            case 4:
                temporal_layer = enc->cfg.ts_layer_id[frame_idx % enc->temporal_layers];
                break;
        }
        enc->current_temporal_layer = temporal_layer;
    }

    if (vpx_codec_encode(&enc->codec, &enc->raw, pts, 1, flags, VPX_DL_REALTIME) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "encode failed");
        return STREAM_VPX_ERROR_CODEC;
    }

    int total_size = 0;
    int is_keyframe = 0;

    const vpx_codec_cx_pkt_t* pkt;
    vpx_codec_iter_t iter = NULL;
    while ((pkt = vpx_codec_get_cx_data(&enc->codec, &iter)) != NULL) {
        if (pkt->kind == VPX_CODEC_CX_FRAME_PKT) {
            if (total_size + (int)pkt->data.frame.sz > out_capacity) {
                set_error("output buffer too small");
                return STREAM_VPX_ERROR_NOMEM;
            }
            memcpy(out_data + total_size, pkt->data.frame.buf, pkt->data.frame.sz);
            total_size += pkt->data.frame.sz;

            if (pkt->data.frame.flags & VPX_FRAME_IS_KEY) {
                is_keyframe = 1;
            }
        }
    }

    if (total_size > 0) {
        enc->frames_encoded++;
        if (is_keyframe) enc->keyframes_encoded++;
        enc->bytes_encoded += total_size;
    }

    if (out_frame_type) *out_frame_type = is_keyframe ? STREAM_VPX_FRAME_KEY : STREAM_VPX_FRAME_DELTA;
    if (out_pts) *out_pts = pts;
    if (out_temporal_layer) *out_temporal_layer = enc->current_temporal_layer;
    if (out_spatial_layer) *out_spatial_layer = enc->current_spatial_layer;

    return total_size;
}

int stream_vpx_encoder_max_output_size(stream_vpx_encoder_t encoder) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc) return 0;
    // Conservative estimate: uncompressed frame size
    return enc->width * enc->height * 3 / 2;
}

void stream_vpx_encoder_request_keyframe(stream_vpx_encoder_t encoder) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (enc) enc->keyframe_requested = 1;
}

int stream_vpx_encoder_set_bitrate(stream_vpx_encoder_t encoder, int bitrate_kbps) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->initialized) {
        set_error("invalid encoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    enc->cfg.rc_target_bitrate = bitrate_kbps;
    if (vpx_codec_enc_config_set(&enc->codec, &enc->cfg) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "failed to set bitrate");
        return STREAM_VPX_ERROR_CODEC;
    }
    return STREAM_VPX_OK;
}

int stream_vpx_encoder_set_temporal_layers(stream_vpx_encoder_t encoder, int layers) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->initialized) {
        set_error("invalid encoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    if (layers < 1 || layers > 4) {
        set_error("temporal layers must be 1-4");
        return STREAM_VPX_ERROR_INVALID;
    }

    if (layers == enc->temporal_layers) {
        return STREAM_VPX_OK; // No change needed
    }

    // Update temporal layer configuration
    int bitrate_kbps = enc->cfg.rc_target_bitrate;
    enc->cfg.ts_number_layers = layers;

    switch (layers) {
        case 1:
            // Single layer - disable temporal scaling
            break;
        case 2:
            enc->cfg.ts_target_bitrate[0] = bitrate_kbps / 2;
            enc->cfg.ts_target_bitrate[1] = bitrate_kbps;
            enc->cfg.ts_rate_decimator[0] = 2;
            enc->cfg.ts_rate_decimator[1] = 1;
            break;
        case 3:
            enc->cfg.ts_target_bitrate[0] = bitrate_kbps / 4;
            enc->cfg.ts_target_bitrate[1] = bitrate_kbps / 2;
            enc->cfg.ts_target_bitrate[2] = bitrate_kbps;
            enc->cfg.ts_rate_decimator[0] = 4;
            enc->cfg.ts_rate_decimator[1] = 2;
            enc->cfg.ts_rate_decimator[2] = 1;
            break;
        case 4:
            enc->cfg.ts_target_bitrate[0] = bitrate_kbps / 8;
            enc->cfg.ts_target_bitrate[1] = bitrate_kbps / 4;
            enc->cfg.ts_target_bitrate[2] = bitrate_kbps / 2;
            enc->cfg.ts_target_bitrate[3] = bitrate_kbps;
            enc->cfg.ts_rate_decimator[0] = 8;
            enc->cfg.ts_rate_decimator[1] = 4;
            enc->cfg.ts_rate_decimator[2] = 2;
            enc->cfg.ts_rate_decimator[3] = 1;
            break;
    }

    enc->cfg.ts_periodicity = layers;
    for (int i = 0; i < layers; i++) {
        enc->cfg.ts_layer_id[i] = i;
    }

    if (vpx_codec_enc_config_set(&enc->codec, &enc->cfg) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "failed to set temporal layers");
        return STREAM_VPX_ERROR_CODEC;
    }

    enc->temporal_layers = layers;
    enc->svc_enabled = (layers > 1 || enc->spatial_layers > 1);

    // Request keyframe to apply changes cleanly
    enc->keyframe_requested = 1;

    return STREAM_VPX_OK;
}

int stream_vpx_encoder_set_spatial_layers(stream_vpx_encoder_t encoder, int layers) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc || !enc->initialized) {
        set_error("invalid encoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    if (enc->codec_type != STREAM_VPX_CODEC_VP9) {
        set_error("spatial layers only supported for VP9");
        return STREAM_VPX_ERROR_INVALID;
    }

    if (layers < 1 || layers > 3) {
        set_error("spatial layers must be 1-3");
        return STREAM_VPX_ERROR_INVALID;
    }

    if (layers == enc->spatial_layers) {
        return STREAM_VPX_OK; // No change needed
    }

    // Update spatial layer configuration
    int bitrate_kbps = enc->cfg.rc_target_bitrate;
    enc->cfg.ss_number_layers = layers;

    switch (layers) {
        case 1:
            break;
        case 2:
            enc->cfg.ss_target_bitrate[0] = bitrate_kbps / 4;
            enc->cfg.ss_target_bitrate[1] = bitrate_kbps;
            break;
        case 3:
            enc->cfg.ss_target_bitrate[0] = bitrate_kbps / 8;
            enc->cfg.ss_target_bitrate[1] = bitrate_kbps / 2;
            enc->cfg.ss_target_bitrate[2] = bitrate_kbps;
            break;
    }

    if (vpx_codec_enc_config_set(&enc->codec, &enc->cfg) != VPX_CODEC_OK) {
        set_vpx_error(&enc->codec, "failed to set spatial layers");
        return STREAM_VPX_ERROR_CODEC;
    }

    enc->spatial_layers = layers;
    enc->svc_enabled = (layers > 1 || enc->temporal_layers > 1);

    // Request keyframe to apply changes cleanly
    enc->keyframe_requested = 1;

    return STREAM_VPX_OK;
}

void stream_vpx_encoder_get_svc_config(
    stream_vpx_encoder_t encoder,
    int* temporal_layers,
    int* spatial_layers,
    int* svc_enabled
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc) return;
    if (temporal_layers) *temporal_layers = enc->temporal_layers;
    if (spatial_layers) *spatial_layers = enc->spatial_layers;
    if (svc_enabled) *svc_enabled = enc->svc_enabled;
}

void stream_vpx_encoder_get_stats(
    stream_vpx_encoder_t encoder,
    uint64_t* frames_encoded,
    uint64_t* keyframes_encoded,
    uint64_t* bytes_encoded
) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc) return;
    if (frames_encoded) *frames_encoded = enc->frames_encoded;
    if (keyframes_encoded) *keyframes_encoded = enc->keyframes_encoded;
    if (bytes_encoded) *bytes_encoded = enc->bytes_encoded;
}

void stream_vpx_encoder_destroy(stream_vpx_encoder_t encoder) {
    encoder_state_t* enc = (encoder_state_t*)(uintptr_t)encoder;
    if (!enc) return;

    if (enc->initialized) {
        vpx_img_free(&enc->raw);
        vpx_codec_destroy(&enc->codec);
    }
    free(enc);
}

// ============================================================================
// Decoder Implementation
// ============================================================================

typedef struct {
    vpx_codec_ctx_t codec;
    int codec_type;
    int initialized;
    int width;
    int height;
    uint64_t frames_decoded;
    uint64_t keyframes_decoded;
    uint64_t bytes_decoded;
    uint64_t corrupted_frames;
} decoder_state_t;

stream_vpx_decoder_t stream_vpx_decoder_create(int codec, int threads) {
    decoder_state_t* dec = (decoder_state_t*)calloc(1, sizeof(decoder_state_t));
    if (!dec) {
        set_error("failed to allocate decoder state");
        return 0;
    }

    vpx_codec_iface_t* iface = NULL;
    switch (codec) {
        case STREAM_VPX_CODEC_VP8:
            iface = vpx_codec_vp8_dx();
            break;
        case STREAM_VPX_CODEC_VP9:
            iface = vpx_codec_vp9_dx();
            break;
        default:
            set_error("invalid codec type");
            free(dec);
            return 0;
    }

    vpx_codec_dec_cfg_t cfg = {0};
    cfg.threads = threads > 0 ? threads : 4;

    if (vpx_codec_dec_init(&dec->codec, iface, &cfg, 0) != VPX_CODEC_OK) {
        set_vpx_error(&dec->codec, "failed to init decoder");
        free(dec);
        return 0;
    }

    dec->codec_type = codec;
    dec->initialized = 1;

    return (stream_vpx_decoder_t)(uintptr_t)dec;
}

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
) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec || !dec->initialized) {
        set_error("invalid decoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    if (vpx_codec_decode(&dec->codec, data, data_len, NULL, 0) != VPX_CODEC_OK) {
        dec->corrupted_frames++;
        set_vpx_error(&dec->codec, "decode failed");
        return STREAM_VPX_ERROR_CODEC;
    }

    vpx_codec_iter_t iter = NULL;
    vpx_image_t* img = vpx_codec_get_frame(&dec->codec, &iter);
    if (!img) {
        return 0;  // Buffering, no frame yet
    }

    dec->width = img->d_w;
    dec->height = img->d_h;
    dec->frames_decoded++;
    dec->bytes_decoded += data_len;

    if (out_y) *out_y = img->planes[0];
    if (out_u) *out_u = img->planes[1];
    if (out_v) *out_v = img->planes[2];
    if (out_y_stride) *out_y_stride = img->stride[0];
    if (out_uv_stride) *out_uv_stride = img->stride[1];
    if (out_width) *out_width = img->d_w;
    if (out_height) *out_height = img->d_h;

    return 1;  // Frame decoded
}

void stream_vpx_decoder_get_dimensions(
    stream_vpx_decoder_t decoder,
    int* width,
    int* height
) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec) return;
    if (width) *width = dec->width;
    if (height) *height = dec->height;
}

void stream_vpx_decoder_get_stats(
    stream_vpx_decoder_t decoder,
    uint64_t* frames_decoded,
    uint64_t* keyframes_decoded,
    uint64_t* bytes_decoded,
    uint64_t* corrupted_frames
) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec) return;
    if (frames_decoded) *frames_decoded = dec->frames_decoded;
    if (keyframes_decoded) *keyframes_decoded = dec->keyframes_decoded;
    if (bytes_decoded) *bytes_decoded = dec->bytes_decoded;
    if (corrupted_frames) *corrupted_frames = dec->corrupted_frames;
}

int stream_vpx_decoder_reset(stream_vpx_decoder_t decoder) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec || !dec->initialized) {
        set_error("invalid decoder handle");
        return STREAM_VPX_ERROR_INVALID;
    }

    // Destroy and recreate
    vpx_codec_iface_t* iface = dec->codec_type == STREAM_VPX_CODEC_VP8
        ? vpx_codec_vp8_dx() : vpx_codec_vp9_dx();

    vpx_codec_destroy(&dec->codec);

    vpx_codec_dec_cfg_t cfg = {0};
    cfg.threads = 4;

    if (vpx_codec_dec_init(&dec->codec, iface, &cfg, 0) != VPX_CODEC_OK) {
        set_vpx_error(&dec->codec, "failed to reset decoder");
        dec->initialized = 0;
        return STREAM_VPX_ERROR_CODEC;
    }

    return STREAM_VPX_OK;
}

void stream_vpx_decoder_destroy(stream_vpx_decoder_t decoder) {
    decoder_state_t* dec = (decoder_state_t*)(uintptr_t)decoder;
    if (!dec) return;

    if (dec->initialized) {
        vpx_codec_destroy(&dec->codec);
    }
    free(dec);
}

// ============================================================================
// Utility
// ============================================================================

const char* stream_vpx_get_error(void) {
    return g_error_msg;
}

int stream_vpx_codec_available(int codec) {
    switch (codec) {
        case STREAM_VPX_CODEC_VP8:
            return vpx_codec_vp8_cx() != NULL && vpx_codec_vp8_dx() != NULL;
        case STREAM_VPX_CODEC_VP9:
            return vpx_codec_vp9_cx() != NULL && vpx_codec_vp9_dx() != NULL;
        default:
            return 0;
    }
}
