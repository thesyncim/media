// stream_h264.c - x264/OpenH264 wrapper implementation
// Thin wrapper around x264 for H.264 encoding and OpenH264 for decoding

#include "stream_h264.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <stdint.h>

// Check for x264 (encoder)
#ifdef __has_include
#if __has_include(<x264.h>)
#define HAS_X264 1
#include <x264.h>
#endif
#endif

#ifndef HAS_X264
#define HAS_X264 0
#endif

// OpenH264 decoder support - always enabled, loaded dynamically via dlopen
// This avoids header conflicts with x264 and doesn't require OpenH264 at build time
#if defined(__APPLE__) || defined(__linux__)
#define HAS_OPENH264 1
#else
#define HAS_OPENH264 0
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

// ============================================================================
// H.264 Decoder Implementation using OpenH264
// ============================================================================

#if HAS_OPENH264

// Use dlopen to load OpenH264 to avoid header conflicts with x264
#include <dlfcn.h>

// OpenH264 types (minimal definitions to avoid including conflicting headers)
typedef void* OH264_DECODER;

// OpenH264 SBufferInfo equivalent (matches codec_api.h)
// IMPORTANT: Must match exactly to avoid memory corruption
typedef struct {
    int iBufferStatus;  // 0: no output, 1: has output
    unsigned long long uiInBsTimeStamp;
    unsigned long long uiOutYuvTimeStamp;
    union {
        struct {
            int iWidth;
            int iHeight;
            int iFormat;
            int iStride[2];
        } sSystemBuffer;
        struct {
            int iSurfaceFormat;
            void* pSurface;
        } sVidSurface;
    } UsrData;
    int iMemoryType;  // 0: CPU, 1: GPU
    unsigned char* pDst[3];  // Y, U, V plane pointers - THIS WAS MISSING!
} OH264BufferInfo;

// OpenH264 decoding states
#define OH264_dsErrorFree 0
#define OH264_dsNoParamSets 4

// OpenH264 SVideoProperty (matches codec_app_def.h)
typedef struct {
    unsigned int size;          // size of the struct
    int eVideoBsType;           // VIDEO_BITSTREAM_TYPE: 0=AVC, 1=SVC
} OH264VideoProperty;

// OpenH264 SDecodingParam (matches codec_app_def.h TagSVCDecodingParam)
typedef struct {
    char* pFileNameRestructed;         // file name for PSNR (can be NULL)
    unsigned int uiCpuLoad;            // CPU load
    unsigned char uiTargetDqLayer;     // target dq layer id
    int eEcActiveIdc;                  // ERROR_CON_IDC: error concealment mode
    unsigned char bParseOnly;          // bool: parse only, no reconstruction
    OH264VideoProperty sVideoProperty; // video stream property
} OH264DecodingParam;

// Function pointer types
typedef int (*pfnWelsCreateDecoder)(OH264_DECODER* ppDecoder);
typedef void (*pfnWelsDestroyDecoder)(OH264_DECODER pDecoder);
typedef int (*pfnInitialize)(OH264_DECODER dec, OH264DecodingParam* pParam);
typedef void (*pfnUninitialize)(OH264_DECODER dec);
typedef int (*pfnDecodeFrameNoDelay)(OH264_DECODER dec, const uint8_t* pSrc, int iSrcLen, uint8_t** ppDst, OH264BufferInfo* pDstInfo);
typedef int (*pfnSetOption)(OH264_DECODER dec, int eOptionId, void* pOption);

// OpenH264 vtable (ISVCDecoderVtbl)
typedef struct {
    int (*Initialize)(OH264_DECODER dec, OH264DecodingParam* pParam);
    int (*Uninitialize)(OH264_DECODER dec);
    int (*DecodeFrame)(OH264_DECODER dec, const uint8_t* pSrc, int iSrcLen, uint8_t** ppDst, int* pStride, int* pWidth, int* pHeight);
    int (*DecodeFrameNoDelay)(OH264_DECODER dec, const uint8_t* pSrc, int iSrcLen, uint8_t** ppDst, OH264BufferInfo* pDstInfo);
    int (*DecodeFrame2)(OH264_DECODER dec, const uint8_t* pSrc, int iSrcLen, uint8_t** ppDst, OH264BufferInfo* pDstInfo);
    int (*FlushFrame)(OH264_DECODER dec, uint8_t** ppDst, OH264BufferInfo* pDstInfo);
    int (*DecodeParser)(OH264_DECODER dec, const uint8_t* pSrc, int iSrcLen, void* pDstInfo);
    int (*DecodeFrameEx)(OH264_DECODER dec, const uint8_t* pSrc, int iSrcLen, uint8_t* pDst, int iDstStride, int* pDstLen, int* pWidth, int* pHeight, int* pColorFormat);
    int (*SetOption)(OH264_DECODER dec, int eOptionId, void* pOption);
    int (*GetOption)(OH264_DECODER dec, int eOptionId, void* pOption);
} OH264DecoderVtbl;

// Global OpenH264 function pointers
static void* g_openh264_lib = NULL;
static pfnWelsCreateDecoder g_WelsCreateDecoder = NULL;
static pfnWelsDestroyDecoder g_WelsDestroyDecoder = NULL;
static int g_openh264_loaded = 0;

static int load_openh264(void) {
    if (g_openh264_loaded) return g_openh264_lib != NULL;
    g_openh264_loaded = 1;

    // Try to load OpenH264
    const char* lib_names[] = {
        "libopenh264.dylib",
        "libopenh264.so",
        "/opt/homebrew/lib/libopenh264.dylib",
        "/opt/homebrew/opt/openh264/lib/libopenh264.dylib",
        "/usr/local/lib/libopenh264.dylib",
        "/usr/lib/libopenh264.so",
        NULL
    };

    for (int i = 0; lib_names[i] != NULL; i++) {
        g_openh264_lib = dlopen(lib_names[i], RTLD_NOW);
        if (g_openh264_lib) break;
    }

    if (!g_openh264_lib) {
        return 0;
    }

    g_WelsCreateDecoder = (pfnWelsCreateDecoder)dlsym(g_openh264_lib, "WelsCreateDecoder");
    g_WelsDestroyDecoder = (pfnWelsDestroyDecoder)dlsym(g_openh264_lib, "WelsDestroyDecoder");

    if (!g_WelsCreateDecoder || !g_WelsDestroyDecoder) {
        dlclose(g_openh264_lib);
        g_openh264_lib = NULL;
        return 0;
    }

    return 1;
}

typedef struct {
    OH264DecoderVtbl** decoder;  // Pointer to vtable pointer

    int width;
    int height;

    // Output buffers (owned by decoder, just pointers)
    uint8_t* y_plane;
    uint8_t* u_plane;
    uint8_t* v_plane;
    int y_stride;
    int uv_stride;

    // Stats
    uint64_t frames_decoded;
    uint64_t keyframes_decoded;
    uint64_t bytes_decoded;
    uint64_t corrupted_frames;
} decoder_state_t;

int stream_h264_decoder_available(void) {
    return load_openh264();
}

stream_h264_decoder_t stream_h264_decoder_create(int threads) {
    if (!load_openh264()) {
        set_error("OpenH264 library not found");
        return 0;
    }

    decoder_state_t* state = calloc(1, sizeof(decoder_state_t));
    if (!state) {
        set_error("Failed to allocate decoder state");
        return 0;
    }

    // Create OpenH264 decoder
    if (g_WelsCreateDecoder(&state->decoder) != 0 || !state->decoder) {
        set_error("Failed to create OpenH264 decoder");
        free(state);
        return 0;
    }

    // Configure decoder
    OH264DecodingParam param = {0};
    param.pFileNameRestructed = NULL;
    param.uiCpuLoad = 0;
    param.uiTargetDqLayer = 0xFF;  // UCHAR_MAX = decode all layers
    param.eEcActiveIdc = 2;        // ERROR_CON_SLICE_COPY
    param.bParseOnly = 0;          // false = do reconstruction
    param.sVideoProperty.size = sizeof(OH264VideoProperty);
    param.sVideoProperty.eVideoBsType = 0;  // VIDEO_BITSTREAM_AVC = 0 (not 1 which is SVC)

    if ((*state->decoder)->Initialize(state->decoder, &param) != 0) {
        set_error("Failed to initialize OpenH264 decoder");
        g_WelsDestroyDecoder(state->decoder);
        free(state);
        return 0;
    }

    // Set threading option if supported
    if (threads > 0) {
        int32_t threadCount = threads;
        (*state->decoder)->SetOption(state->decoder, 6, &threadCount);  // DECODER_OPTION_NUM_OF_THREADS = 6
    }

    return (stream_h264_decoder_t)(uintptr_t)state;
}

int stream_h264_decoder_decode(
    stream_h264_decoder_t decoder,
    const uint8_t* data,
    int len,
    const uint8_t** out_y,
    const uint8_t** out_u,
    const uint8_t** out_v,
    int* out_y_stride,
    int* out_uv_stride,
    int* out_width,
    int* out_height
) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state || !state->decoder) {
        set_error("Invalid decoder handle");
        return STREAM_H264_ERROR_INVALID;
    }

    if (!data || len <= 0) {
        set_error("Invalid input data");
        return STREAM_H264_ERROR_INVALID;
    }

    // Decode frame
    uint8_t* dst[3] = {NULL, NULL, NULL};
    OH264BufferInfo bufInfo;
    memset(&bufInfo, 0, sizeof(bufInfo));

    int ret = (*state->decoder)->DecodeFrameNoDelay(state->decoder, data, len, dst, &bufInfo);

    if (ret != OH264_dsErrorFree) {
        if (ret == OH264_dsNoParamSets) {
            // Need more data - not fatal
            return 0;
        }
        state->corrupted_frames++;
        return 0;  // Try to continue
    }

    // Check if we got output
    if (bufInfo.iBufferStatus != 1) {
        return 0;  // No output yet (buffering)
    }

    // Update state with decoded frame info
    state->width = bufInfo.UsrData.sSystemBuffer.iWidth;
    state->height = bufInfo.UsrData.sSystemBuffer.iHeight;
    state->y_stride = bufInfo.UsrData.sSystemBuffer.iStride[0];
    state->uv_stride = bufInfo.UsrData.sSystemBuffer.iStride[1];

    // Use dst array directly - DecodeFrameNoDelay fills this with Y, U, V pointers
    state->y_plane = dst[0];
    state->u_plane = dst[1];
    state->v_plane = dst[2];

    // Output pointers
    if (out_y) *out_y = state->y_plane;
    if (out_u) *out_u = state->u_plane;
    if (out_v) *out_v = state->v_plane;
    if (out_y_stride) *out_y_stride = state->y_stride;
    if (out_uv_stride) *out_uv_stride = state->uv_stride;
    if (out_width) *out_width = state->width;
    if (out_height) *out_height = state->height;

    // Update stats
    state->frames_decoded++;
    state->bytes_decoded += len;

    // Check if keyframe (simplified - check NAL type)
    if (len >= 5) {
        int nal_type = data[4] & 0x1F;
        if (nal_type == 5 || nal_type == 7) {  // IDR or SPS
            state->keyframes_decoded++;
        }
    }

    // Return 1 to indicate success with output (Go code checks result > 0)
    return 1;
}

void stream_h264_decoder_get_dimensions(stream_h264_decoder_t decoder, int* width, int* height) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) return;

    if (width) *width = state->width;
    if (height) *height = state->height;
}

void stream_h264_decoder_get_stats(
    stream_h264_decoder_t decoder,
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

int stream_h264_decoder_reset(stream_h264_decoder_t decoder) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state || !state->decoder) {
        return STREAM_H264_ERROR_INVALID;
    }

    // Reinitialize decoder
    OH264DecodingParam param = {0};
    param.pFileNameRestructed = NULL;
    param.uiCpuLoad = 0;
    param.uiTargetDqLayer = 0xFF;  // UCHAR_MAX = decode all layers
    param.eEcActiveIdc = 2;        // ERROR_CON_SLICE_COPY
    param.bParseOnly = 0;          // false = do reconstruction
    param.sVideoProperty.size = sizeof(OH264VideoProperty);
    param.sVideoProperty.eVideoBsType = 0;  // VIDEO_BITSTREAM_AVC = 0 (not 1 which is SVC)

    (*state->decoder)->Uninitialize(state->decoder);
    if ((*state->decoder)->Initialize(state->decoder, &param) != 0) {
        set_error("Failed to reset decoder");
        return STREAM_H264_ERROR_CODEC;
    }

    state->width = 0;
    state->height = 0;
    state->y_plane = NULL;
    state->u_plane = NULL;
    state->v_plane = NULL;

    return STREAM_H264_OK;
}

void stream_h264_decoder_destroy(stream_h264_decoder_t decoder) {
    decoder_state_t* state = (decoder_state_t*)(uintptr_t)decoder;
    if (!state) return;

    if (state->decoder) {
        (*state->decoder)->Uninitialize(state->decoder);
        g_WelsDestroyDecoder(state->decoder);
    }

    free(state);
}

#else // !HAS_OPENH264

// Decoder stubs when OpenH264 is not available
int stream_h264_decoder_available(void) { return 0; }
stream_h264_decoder_t stream_h264_decoder_create(int threads) {
    set_error("OpenH264 decoder not available - install openh264 and rebuild");
    return 0;
}
int stream_h264_decoder_decode(stream_h264_decoder_t d, const uint8_t* data, int len,
    const uint8_t** y, const uint8_t** u, const uint8_t** v, int* ys, int* uvs, int* w, int* h) { return STREAM_H264_ERROR; }
void stream_h264_decoder_get_dimensions(stream_h264_decoder_t d, int* w, int* h) {}
void stream_h264_decoder_get_stats(stream_h264_decoder_t d, uint64_t* f, uint64_t* k, uint64_t* b, uint64_t* c) {}
int stream_h264_decoder_reset(stream_h264_decoder_t d) { return STREAM_H264_ERROR; }
void stream_h264_decoder_destroy(stream_h264_decoder_t d) {}

#endif // HAS_OPENH264
