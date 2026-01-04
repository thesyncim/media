// media_compositor.c - High-performance video compositing
//
// SIMD-optimized blending for ARM NEON and x86 SSE/AVX.
// Compile: cc -shared -fPIC -O3 -o libmedia_compositor.dylib media_compositor.c

#include "media_compositor.h"
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#if defined(__ARM_NEON) || defined(__ARM_NEON__)
#include <arm_neon.h>
#define USE_NEON 1
#elif defined(__SSE2__)
#include <emmintrin.h>
#define USE_SSE2 1
#endif

// Compositor structure
struct MediaCompositor {
    int32_t width;
    int32_t height;
    int32_t stride_y;
    int32_t stride_uv;
    uint8_t* canvas_y;
    uint8_t* canvas_u;
    uint8_t* canvas_v;
    // Scratch buffers for scaling
    uint8_t* scratch_y;
    uint8_t* scratch_u;
    uint8_t* scratch_v;
    int32_t scratch_capacity;
};

// Helper: clamp value to 0-255
static inline uint8_t clamp255(int32_t v) {
    return v < 0 ? 0 : (v > 255 ? 255 : v);
}

// Helper: alpha blend single value
static inline uint8_t blend_over(uint8_t dst, uint8_t src, uint8_t alpha) {
    // out = src * alpha + dst * (1 - alpha)
    // Using fixed-point: (src * alpha + dst * (255 - alpha) + 127) / 255
    int32_t result = ((int32_t)src * alpha + (int32_t)dst * (255 - alpha) + 127) / 255;
    return (uint8_t)result;
}

// Helper: additive blend
static inline uint8_t blend_add(uint8_t dst, uint8_t src, uint8_t alpha) {
    int32_t result = (int32_t)dst + ((int32_t)src * alpha) / 255;
    return clamp255(result);
}

// Helper: multiply blend
static inline uint8_t blend_multiply(uint8_t dst, uint8_t src, uint8_t alpha) {
    int32_t blended = ((int32_t)dst * (int32_t)src) / 255;
    return blend_over(dst, blended, alpha);
}

MediaCompositor* media_compositor_create(int32_t width, int32_t height) {
    MediaCompositor* comp = (MediaCompositor*)calloc(1, sizeof(MediaCompositor));
    if (!comp) return NULL;

    // Ensure dimensions are even for YUV420
    comp->width = (width + 1) & ~1;
    comp->height = (height + 1) & ~1;

    // Align strides for SIMD (16-byte alignment)
    comp->stride_y = (comp->width + 15) & ~15;
    comp->stride_uv = ((comp->width / 2) + 15) & ~15;

    // Allocate canvas buffers
    size_t size_y = comp->stride_y * comp->height;
    size_t size_uv = comp->stride_uv * (comp->height / 2);

    comp->canvas_y = (uint8_t*)aligned_alloc(16, size_y);
    comp->canvas_u = (uint8_t*)aligned_alloc(16, size_uv);
    comp->canvas_v = (uint8_t*)aligned_alloc(16, size_uv);

    if (!comp->canvas_y || !comp->canvas_u || !comp->canvas_v) {
        media_compositor_destroy(comp);
        return NULL;
    }

    // Initialize to black
    media_compositor_clear(comp, 16, 128, 128);

    return comp;
}

void media_compositor_destroy(MediaCompositor* comp) {
    if (!comp) return;
    free(comp->canvas_y);
    free(comp->canvas_u);
    free(comp->canvas_v);
    free(comp->scratch_y);
    free(comp->scratch_u);
    free(comp->scratch_v);
    free(comp);
}

void media_compositor_clear(MediaCompositor* comp, uint8_t y, uint8_t u, uint8_t v) {
    if (!comp) return;

    size_t size_y = comp->stride_y * comp->height;
    size_t size_uv = comp->stride_uv * (comp->height / 2);

    memset(comp->canvas_y, y, size_y);
    memset(comp->canvas_u, u, size_uv);
    memset(comp->canvas_v, v, size_uv);
}

void media_fill_yuv(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v,
    int32_t x, int32_t y, int32_t w, int32_t h,
    int32_t stride_y, int32_t stride_uv,
    uint8_t fill_y, uint8_t fill_u, uint8_t fill_v
) {
    // Fill Y plane
    for (int32_t row = 0; row < h; row++) {
        memset(dst_y + (y + row) * stride_y + x, fill_y, w);
    }

    // Fill U and V planes (half resolution)
    int32_t uv_x = x / 2;
    int32_t uv_y = y / 2;
    int32_t uv_w = w / 2;
    int32_t uv_h = h / 2;

    for (int32_t row = 0; row < uv_h; row++) {
        memset(dst_u + (uv_y + row) * stride_uv + uv_x, fill_u, uv_w);
        memset(dst_v + (uv_y + row) * stride_uv + uv_x, fill_v, uv_w);
    }
}

// SIMD-optimized alpha blending for NEON
#ifdef USE_NEON
static void blend_row_neon(uint8_t* dst, const uint8_t* src, const uint8_t* alpha,
                           uint8_t global_alpha, int32_t width, MediaBlendMode mode) {
    int32_t i = 0;

    // Process 16 pixels at a time
    for (; i + 16 <= width; i += 16) {
        uint8x16_t d = vld1q_u8(dst + i);
        uint8x16_t s = vld1q_u8(src + i);
        uint8x16_t a;

        if (alpha) {
            a = vld1q_u8(alpha + i);
            // Multiply with global alpha: (a * global_alpha + 127) / 255
            uint16x8_t a_lo = vmull_u8(vget_low_u8(a), vdup_n_u8(global_alpha));
            uint16x8_t a_hi = vmull_u8(vget_high_u8(a), vdup_n_u8(global_alpha));
            a = vcombine_u8(
                vshrn_n_u16(vaddq_u16(a_lo, vdupq_n_u16(127)), 8),
                vshrn_n_u16(vaddq_u16(a_hi, vdupq_n_u16(127)), 8)
            );
        } else {
            a = vdupq_n_u8(global_alpha);
        }

        // Compute 255 - alpha
        uint8x16_t inv_a = vsubq_u8(vdupq_n_u8(255), a);

        // Blend: (src * alpha + dst * (255 - alpha) + 127) / 255
        // Split into low/high for 16-bit math
        uint16x8_t r_lo = vmull_u8(vget_low_u8(s), vget_low_u8(a));
        uint16x8_t r_hi = vmull_u8(vget_high_u8(s), vget_high_u8(a));
        r_lo = vmlal_u8(r_lo, vget_low_u8(d), vget_low_u8(inv_a));
        r_hi = vmlal_u8(r_hi, vget_high_u8(d), vget_high_u8(inv_a));
        r_lo = vaddq_u16(r_lo, vdupq_n_u16(127));
        r_hi = vaddq_u16(r_hi, vdupq_n_u16(127));

        uint8x16_t result = vcombine_u8(vshrn_n_u16(r_lo, 8), vshrn_n_u16(r_hi, 8));
        vst1q_u8(dst + i, result);
    }

    // Scalar fallback for remaining pixels
    for (; i < width; i++) {
        uint8_t a = alpha ? ((alpha[i] * global_alpha + 127) / 255) : global_alpha;
        dst[i] = blend_over(dst[i], src[i], a);
    }
}
#endif

// SSE2 version
#ifdef USE_SSE2
static void blend_row_sse2(uint8_t* dst, const uint8_t* src, const uint8_t* alpha,
                           uint8_t global_alpha, int32_t width, MediaBlendMode mode) {
    int32_t i = 0;
    __m128i zero = _mm_setzero_si128();
    __m128i ga = _mm_set1_epi16(global_alpha);
    __m128i c255 = _mm_set1_epi16(255);
    __m128i c127 = _mm_set1_epi16(127);

    for (; i + 16 <= width; i += 16) {
        __m128i d = _mm_loadu_si128((__m128i*)(dst + i));
        __m128i s = _mm_loadu_si128((__m128i*)(src + i));
        __m128i a;

        if (alpha) {
            a = _mm_loadu_si128((__m128i*)(alpha + i));
            // Multiply with global alpha
            __m128i a_lo = _mm_unpacklo_epi8(a, zero);
            __m128i a_hi = _mm_unpackhi_epi8(a, zero);
            a_lo = _mm_mullo_epi16(a_lo, ga);
            a_hi = _mm_mullo_epi16(a_hi, ga);
            a_lo = _mm_add_epi16(a_lo, c127);
            a_hi = _mm_add_epi16(a_hi, c127);
            a_lo = _mm_srli_epi16(a_lo, 8);
            a_hi = _mm_srli_epi16(a_hi, 8);
            a = _mm_packus_epi16(a_lo, a_hi);
        } else {
            a = _mm_set1_epi8(global_alpha);
        }

        // Unpack to 16-bit
        __m128i d_lo = _mm_unpacklo_epi8(d, zero);
        __m128i d_hi = _mm_unpackhi_epi8(d, zero);
        __m128i s_lo = _mm_unpacklo_epi8(s, zero);
        __m128i s_hi = _mm_unpackhi_epi8(s, zero);
        __m128i a_lo = _mm_unpacklo_epi8(a, zero);
        __m128i a_hi = _mm_unpackhi_epi8(a, zero);
        __m128i inv_a_lo = _mm_sub_epi16(c255, a_lo);
        __m128i inv_a_hi = _mm_sub_epi16(c255, a_hi);

        // Blend
        __m128i r_lo = _mm_add_epi16(_mm_mullo_epi16(s_lo, a_lo), _mm_mullo_epi16(d_lo, inv_a_lo));
        __m128i r_hi = _mm_add_epi16(_mm_mullo_epi16(s_hi, a_hi), _mm_mullo_epi16(d_hi, inv_a_hi));
        r_lo = _mm_add_epi16(r_lo, c127);
        r_hi = _mm_add_epi16(r_hi, c127);
        r_lo = _mm_srli_epi16(r_lo, 8);
        r_hi = _mm_srli_epi16(r_hi, 8);

        __m128i result = _mm_packus_epi16(r_lo, r_hi);
        _mm_storeu_si128((__m128i*)(dst + i), result);
    }

    // Scalar fallback
    for (; i < width; i++) {
        uint8_t a = alpha ? ((alpha[i] * global_alpha + 127) / 255) : global_alpha;
        dst[i] = blend_over(dst[i], src[i], a);
    }
}
#endif

// Scalar blending fallback
static void blend_row_scalar(uint8_t* dst, const uint8_t* src, const uint8_t* alpha,
                             uint8_t global_alpha, int32_t width, MediaBlendMode mode) {
    for (int32_t i = 0; i < width; i++) {
        uint8_t a = alpha ? ((alpha[i] * global_alpha + 127) / 255) : global_alpha;

        switch (mode) {
            case BLEND_MODE_COPY:
                dst[i] = src[i];
                break;
            case BLEND_MODE_OVER:
                dst[i] = blend_over(dst[i], src[i], a);
                break;
            case BLEND_MODE_ADD:
                dst[i] = blend_add(dst[i], src[i], a);
                break;
            case BLEND_MODE_MULTIPLY:
                dst[i] = blend_multiply(dst[i], src[i], a);
                break;
        }
    }
}

// Choose best blend function
static void blend_row(uint8_t* dst, const uint8_t* src, const uint8_t* alpha,
                      uint8_t global_alpha, int32_t width, MediaBlendMode mode) {
    if (mode == BLEND_MODE_COPY && global_alpha == 255) {
        memcpy(dst, src, width);
        return;
    }

#ifdef USE_NEON
    if (mode == BLEND_MODE_OVER) {
        blend_row_neon(dst, src, alpha, global_alpha, width, mode);
        return;
    }
#endif
#ifdef USE_SSE2
    if (mode == BLEND_MODE_OVER) {
        blend_row_sse2(dst, src, alpha, global_alpha, width, mode);
        return;
    }
#endif
    blend_row_scalar(dst, src, alpha, global_alpha, width, mode);
}

void media_blend_yuv_alpha(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v,
    const uint8_t* src_y, const uint8_t* src_u, const uint8_t* src_v,
    const uint8_t* alpha,
    uint8_t global_alpha,
    int32_t x, int32_t y,
    int32_t w, int32_t h,
    int32_t dst_stride_y, int32_t dst_stride_uv,
    int32_t src_stride_y, int32_t src_stride_uv,
    MediaBlendMode mode
) {
    // Ensure even dimensions
    w = w & ~1;
    h = h & ~1;
    x = x & ~1;
    y = y & ~1;

    // Blend Y plane
    for (int32_t row = 0; row < h; row++) {
        uint8_t* d = dst_y + (y + row) * dst_stride_y + x;
        const uint8_t* s = src_y + row * src_stride_y;
        const uint8_t* a = alpha ? (alpha + row * w) : NULL;
        blend_row(d, s, a, global_alpha, w, mode);
    }

    // Blend U and V planes (subsampled)
    int32_t uv_x = x / 2;
    int32_t uv_y = y / 2;
    int32_t uv_w = w / 2;
    int32_t uv_h = h / 2;

    for (int32_t row = 0; row < uv_h; row++) {
        uint8_t* du = dst_u + (uv_y + row) * dst_stride_uv + uv_x;
        uint8_t* dv = dst_v + (uv_y + row) * dst_stride_uv + uv_x;
        const uint8_t* su = src_u + row * src_stride_uv;
        const uint8_t* sv = src_v + row * src_stride_uv;

        // Subsample alpha for UV planes
        const uint8_t* a = NULL;
        uint8_t alpha_sub[4096]; // Stack buffer for subsampled alpha
        if (alpha) {
            for (int32_t i = 0; i < uv_w && i < 4096; i++) {
                // Average 2x2 block of alpha
                int32_t src_row = row * 2;
                int32_t src_col = i * 2;
                int32_t sum = alpha[src_row * w + src_col] +
                              alpha[src_row * w + src_col + 1] +
                              alpha[(src_row + 1) * w + src_col] +
                              alpha[(src_row + 1) * w + src_col + 1];
                alpha_sub[i] = (sum + 2) / 4;
            }
            a = alpha_sub;
        }

        blend_row(du, su, a, global_alpha, uv_w, mode);
        blend_row(dv, sv, a, global_alpha, uv_w, mode);
    }
}

// Bilinear scaling
void media_scale_yuv_bilinear(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v,
    const uint8_t* src_y, const uint8_t* src_u, const uint8_t* src_v,
    int32_t src_w, int32_t src_h,
    int32_t dst_w, int32_t dst_h,
    int32_t dst_stride_y, int32_t dst_stride_uv,
    int32_t src_stride_y, int32_t src_stride_uv
) {
    // Fixed-point scaling factors (16.16)
    uint32_t x_ratio = ((src_w - 1) << 16) / (dst_w > 1 ? dst_w - 1 : 1);
    uint32_t y_ratio = ((src_h - 1) << 16) / (dst_h > 1 ? dst_h - 1 : 1);

    // Scale Y plane
    for (int32_t j = 0; j < dst_h; j++) {
        uint32_t y_pos = (j * y_ratio);
        int32_t y0 = y_pos >> 16;
        int32_t y1 = y0 + 1;
        if (y1 >= src_h) y1 = src_h - 1;
        uint32_t y_frac = y_pos & 0xFFFF;

        for (int32_t i = 0; i < dst_w; i++) {
            uint32_t x_pos = (i * x_ratio);
            int32_t x0 = x_pos >> 16;
            int32_t x1 = x0 + 1;
            if (x1 >= src_w) x1 = src_w - 1;
            uint32_t x_frac = x_pos & 0xFFFF;

            // Get 4 source pixels
            uint8_t p00 = src_y[y0 * src_stride_y + x0];
            uint8_t p01 = src_y[y0 * src_stride_y + x1];
            uint8_t p10 = src_y[y1 * src_stride_y + x0];
            uint8_t p11 = src_y[y1 * src_stride_y + x1];

            // Bilinear interpolation
            uint32_t top = ((0x10000 - x_frac) * p00 + x_frac * p01) >> 16;
            uint32_t bot = ((0x10000 - x_frac) * p10 + x_frac * p11) >> 16;
            uint32_t result = ((0x10000 - y_frac) * top + y_frac * bot) >> 16;

            dst_y[j * dst_stride_y + i] = (uint8_t)result;
        }
    }

    // Scale U and V planes (half resolution)
    int32_t src_uv_w = src_w / 2;
    int32_t src_uv_h = src_h / 2;
    int32_t dst_uv_w = dst_w / 2;
    int32_t dst_uv_h = dst_h / 2;

    x_ratio = ((src_uv_w - 1) << 16) / (dst_uv_w > 1 ? dst_uv_w - 1 : 1);
    y_ratio = ((src_uv_h - 1) << 16) / (dst_uv_h > 1 ? dst_uv_h - 1 : 1);

    for (int32_t j = 0; j < dst_uv_h; j++) {
        uint32_t y_pos = (j * y_ratio);
        int32_t y0 = y_pos >> 16;
        int32_t y1 = y0 + 1;
        if (y1 >= src_uv_h) y1 = src_uv_h - 1;
        uint32_t y_frac = y_pos & 0xFFFF;

        for (int32_t i = 0; i < dst_uv_w; i++) {
            uint32_t x_pos = (i * x_ratio);
            int32_t x0 = x_pos >> 16;
            int32_t x1 = x0 + 1;
            if (x1 >= src_uv_w) x1 = src_uv_w - 1;
            uint32_t x_frac = x_pos & 0xFFFF;

            // U plane
            {
                uint8_t p00 = src_u[y0 * src_stride_uv + x0];
                uint8_t p01 = src_u[y0 * src_stride_uv + x1];
                uint8_t p10 = src_u[y1 * src_stride_uv + x0];
                uint8_t p11 = src_u[y1 * src_stride_uv + x1];
                uint32_t top = ((0x10000 - x_frac) * p00 + x_frac * p01) >> 16;
                uint32_t bot = ((0x10000 - x_frac) * p10 + x_frac * p11) >> 16;
                dst_u[j * dst_stride_uv + i] = (uint8_t)(((0x10000 - y_frac) * top + y_frac * bot) >> 16);
            }

            // V plane
            {
                uint8_t p00 = src_v[y0 * src_stride_uv + x0];
                uint8_t p01 = src_v[y0 * src_stride_uv + x1];
                uint8_t p10 = src_v[y1 * src_stride_uv + x0];
                uint8_t p11 = src_v[y1 * src_stride_uv + x1];
                uint32_t top = ((0x10000 - x_frac) * p00 + x_frac * p01) >> 16;
                uint32_t bot = ((0x10000 - x_frac) * p10 + x_frac * p11) >> 16;
                dst_v[j * dst_stride_uv + i] = (uint8_t)(((0x10000 - y_frac) * top + y_frac * bot) >> 16);
            }
        }
    }
}

// Ensure scratch buffer is large enough
static void ensure_scratch(MediaCompositor* comp, int32_t w, int32_t h) {
    int32_t stride_y = (w + 15) & ~15;
    int32_t stride_uv = ((w / 2) + 15) & ~15;
    size_t needed = stride_y * h + stride_uv * (h / 2) * 2;

    if (comp->scratch_capacity < needed) {
        free(comp->scratch_y);
        free(comp->scratch_u);
        free(comp->scratch_v);

        comp->scratch_y = (uint8_t*)aligned_alloc(16, stride_y * h);
        comp->scratch_u = (uint8_t*)aligned_alloc(16, stride_uv * (h / 2));
        comp->scratch_v = (uint8_t*)aligned_alloc(16, stride_uv * (h / 2));
        comp->scratch_capacity = needed;
    }
}

void media_compositor_blend_layer(
    MediaCompositor* comp,
    const uint8_t* src_y, const uint8_t* src_u, const uint8_t* src_v,
    const uint8_t* src_a,
    int32_t src_w, int32_t src_h,
    int32_t src_stride_y, int32_t src_stride_uv,
    const MediaLayerConfig* config
) {
    if (!comp || !config || !config->visible) return;
    if (!src_y || !src_u || !src_v) return;

    int32_t dst_w = config->width > 0 ? config->width : src_w;
    int32_t dst_h = config->height > 0 ? config->height : src_h;

    // Clamp to canvas bounds
    int32_t x = config->x;
    int32_t y = config->y;
    if (x >= comp->width || y >= comp->height) return;
    if (x + dst_w <= 0 || y + dst_h <= 0) return;

    // Clip to canvas
    if (x < 0) { dst_w += x; x = 0; }
    if (y < 0) { dst_h += y; y = 0; }
    if (x + dst_w > comp->width) dst_w = comp->width - x;
    if (y + dst_h > comp->height) dst_h = comp->height - y;

    // Ensure even dimensions
    dst_w = dst_w & ~1;
    dst_h = dst_h & ~1;
    x = x & ~1;
    y = y & ~1;

    if (dst_w <= 0 || dst_h <= 0) return;

    const uint8_t* blend_y = src_y;
    const uint8_t* blend_u = src_u;
    const uint8_t* blend_v = src_v;
    int32_t blend_stride_y = src_stride_y;
    int32_t blend_stride_uv = src_stride_uv;

    // Scale if needed
    if (dst_w != src_w || dst_h != src_h) {
        ensure_scratch(comp, dst_w, dst_h);

        int32_t scratch_stride_y = (dst_w + 15) & ~15;
        int32_t scratch_stride_uv = ((dst_w / 2) + 15) & ~15;

        media_scale_yuv_bilinear(
            comp->scratch_y, comp->scratch_u, comp->scratch_v,
            src_y, src_u, src_v,
            src_w, src_h, dst_w, dst_h,
            scratch_stride_y, scratch_stride_uv,
            src_stride_y, src_stride_uv
        );

        blend_y = comp->scratch_y;
        blend_u = comp->scratch_u;
        blend_v = comp->scratch_v;
        blend_stride_y = scratch_stride_y;
        blend_stride_uv = scratch_stride_uv;
    }

    // Convert alpha 0.0-1.0 to 0-255
    uint8_t global_alpha = (uint8_t)(config->alpha * 255.0f);
    if (global_alpha == 0) return;

    media_blend_yuv_alpha(
        comp->canvas_y, comp->canvas_u, comp->canvas_v,
        blend_y, blend_u, blend_v,
        src_a,
        global_alpha,
        x, y, dst_w, dst_h,
        comp->stride_y, comp->stride_uv,
        blend_stride_y, blend_stride_uv,
        config->blend_mode
    );
}

void media_compositor_get_result(
    MediaCompositor* comp,
    const uint8_t** out_y, const uint8_t** out_u, const uint8_t** out_v,
    int32_t* out_stride_y, int32_t* out_stride_uv
) {
    if (!comp) return;
    if (out_y) *out_y = comp->canvas_y;
    if (out_u) *out_u = comp->canvas_u;
    if (out_v) *out_v = comp->canvas_v;
    if (out_stride_y) *out_stride_y = comp->stride_y;
    if (out_stride_uv) *out_stride_uv = comp->stride_uv;
}

void media_compositor_get_size(MediaCompositor* comp, int32_t* width, int32_t* height) {
    if (!comp) return;
    if (width) *width = comp->width;
    if (height) *height = comp->height;
}

// RGBA to YUVA conversion using BT.601
void media_rgba_to_yuva(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v, uint8_t* dst_a,
    const uint8_t* src_rgba,
    int32_t width, int32_t height,
    int32_t dst_stride_y, int32_t dst_stride_uv,
    int32_t src_stride
) {
    for (int32_t j = 0; j < height; j++) {
        const uint8_t* src_row = src_rgba + j * src_stride;
        uint8_t* y_row = dst_y + j * dst_stride_y;
        uint8_t* a_row = dst_a + j * width;

        for (int32_t i = 0; i < width; i++) {
            int32_t r = src_row[i * 4 + 0];
            int32_t g = src_row[i * 4 + 1];
            int32_t b = src_row[i * 4 + 2];
            int32_t a = src_row[i * 4 + 3];

            // BT.601 conversion
            int32_t y = ((66 * r + 129 * g + 25 * b + 128) >> 8) + 16;
            y_row[i] = clamp255(y);
            a_row[i] = a;
        }
    }

    // Subsample UV
    for (int32_t j = 0; j < height / 2; j++) {
        const uint8_t* src_row0 = src_rgba + (j * 2) * src_stride;
        const uint8_t* src_row1 = src_rgba + (j * 2 + 1) * src_stride;
        uint8_t* u_row = dst_u + j * dst_stride_uv;
        uint8_t* v_row = dst_v + j * dst_stride_uv;

        for (int32_t i = 0; i < width / 2; i++) {
            // Average 2x2 block
            int32_t r = (src_row0[i*8+0] + src_row0[i*8+4] + src_row1[i*8+0] + src_row1[i*8+4]) / 4;
            int32_t g = (src_row0[i*8+1] + src_row0[i*8+5] + src_row1[i*8+1] + src_row1[i*8+5]) / 4;
            int32_t b = (src_row0[i*8+2] + src_row0[i*8+6] + src_row1[i*8+2] + src_row1[i*8+6]) / 4;

            int32_t u = ((-38 * r - 74 * g + 112 * b + 128) >> 8) + 128;
            int32_t v = ((112 * r - 94 * g - 18 * b + 128) >> 8) + 128;

            u_row[i] = clamp255(u);
            v_row[i] = clamp255(v);
        }
    }
}
