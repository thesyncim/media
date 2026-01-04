// stream_compositor.h - High-performance video compositing library
//
// Provides SIMD-optimized blending operations for video compositing.
// Supports YUV420 (I420) format with optional alpha plane.

#ifndef STREAM_COMPOSITOR_H
#define STREAM_COMPOSITOR_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Blend modes
typedef enum {
    BLEND_MODE_COPY = 0,      // Direct copy (ignore alpha)
    BLEND_MODE_OVER,          // Porter-Duff over (standard alpha blend)
    BLEND_MODE_ADD,           // Additive blending
    BLEND_MODE_MULTIPLY,      // Multiply blending
} StreamBlendMode;

// Compositor handle
typedef struct StreamCompositor StreamCompositor;

// Layer configuration
typedef struct {
    int32_t x;                // X position on canvas
    int32_t y;                // Y position on canvas
    int32_t width;            // Scaled width (0 = use source width)
    int32_t height;           // Scaled height (0 = use source height)
    int32_t z_order;          // Layer order (higher = on top)
    float alpha;              // Layer opacity 0.0-1.0
    int32_t visible;          // Layer visibility
    StreamBlendMode blend_mode;
} StreamLayerConfig;

// Create compositor with canvas dimensions
StreamCompositor* stream_compositor_create(int32_t width, int32_t height);

// Destroy compositor
void stream_compositor_destroy(StreamCompositor* comp);

// Clear canvas to a solid color (YUV)
void stream_compositor_clear(StreamCompositor* comp, uint8_t y, uint8_t u, uint8_t v);

// Blend a layer onto the canvas
// src_y/u/v: Source YUV planes
// src_a: Source alpha plane (NULL for opaque, values 0-255)
// src_w, src_h: Source dimensions
// src_stride_y, src_stride_uv: Source strides
// config: Layer configuration
void stream_compositor_blend_layer(
    StreamCompositor* comp,
    const uint8_t* src_y, const uint8_t* src_u, const uint8_t* src_v,
    const uint8_t* src_a,
    int32_t src_w, int32_t src_h,
    int32_t src_stride_y, int32_t src_stride_uv,
    const StreamLayerConfig* config
);

// Get the composed result
// Returns pointers to internal buffers (valid until next clear/blend operation)
void stream_compositor_get_result(
    StreamCompositor* comp,
    const uint8_t** out_y, const uint8_t** out_u, const uint8_t** out_v,
    int32_t* out_stride_y, int32_t* out_stride_uv
);

// Get canvas dimensions
void stream_compositor_get_size(StreamCompositor* comp, int32_t* width, int32_t* height);

// ----- Low-level blending functions (for direct use) -----

// Blend a region with alpha
// dst/src: Y, U, V planes
// alpha: Per-pixel alpha (0-255) or NULL for global_alpha only
// global_alpha: Global layer alpha (0-255)
// x, y: Destination position
// w, h: Region dimensions
// dst_stride, src_stride: Strides
void stream_blend_yuv_alpha(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v,
    const uint8_t* src_y, const uint8_t* src_u, const uint8_t* src_v,
    const uint8_t* alpha,
    uint8_t global_alpha,
    int32_t x, int32_t y,
    int32_t w, int32_t h,
    int32_t dst_stride_y, int32_t dst_stride_uv,
    int32_t src_stride_y, int32_t src_stride_uv,
    StreamBlendMode mode
);

// Scale YUV frame using bilinear interpolation
// dst/src: Y, U, V planes
// src_w, src_h: Source dimensions
// dst_w, dst_h: Destination dimensions
// dst_stride, src_stride: Strides
void stream_scale_yuv_bilinear(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v,
    const uint8_t* src_y, const uint8_t* src_u, const uint8_t* src_v,
    int32_t src_w, int32_t src_h,
    int32_t dst_w, int32_t dst_h,
    int32_t dst_stride_y, int32_t dst_stride_uv,
    int32_t src_stride_y, int32_t src_stride_uv
);

// Fill a region with solid YUV color
void stream_fill_yuv(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v,
    int32_t x, int32_t y, int32_t w, int32_t h,
    int32_t stride_y, int32_t stride_uv,
    uint8_t fill_y, uint8_t fill_u, uint8_t fill_v
);

// Convert RGBA to YUVA
void stream_rgba_to_yuva(
    uint8_t* dst_y, uint8_t* dst_u, uint8_t* dst_v, uint8_t* dst_a,
    const uint8_t* src_rgba,
    int32_t width, int32_t height,
    int32_t dst_stride_y, int32_t dst_stride_uv,
    int32_t src_stride
);

#ifdef __cplusplus
}
#endif

#endif // STREAM_COMPOSITOR_H
