// media_v4l2.c - V4L2 wrapper implementation for Linux
//
// Compile with:
//   cc -shared -fPIC -O2 -o libmedia_v4l2.so media_v4l2.c -lpthread

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <errno.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <dirent.h>
#include <pthread.h>
#include <stdarg.h>
#include <time.h>
#include <linux/videodev2.h>

#include "media_v4l2.h"

// Thread-local error buffer
static __thread char error_buffer[256] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(error_buffer, sizeof(error_buffer), fmt, args);
    va_end(args);
}

const char* media_v4l2_get_error(void) {
    return error_buffer;
}

// YUYV to I420 conversion
static void yuyv_to_i420(const uint8_t* yuyv, int width, int height,
                         uint8_t* y_out, uint8_t* u_out, uint8_t* v_out) {
    int y_stride = width;
    int uv_stride = width / 2;

    for (int row = 0; row < height; row++) {
        const uint8_t* yuyv_row = yuyv + row * width * 2;
        uint8_t* y_row = y_out + row * y_stride;

        // Extract Y values
        for (int col = 0; col < width; col++) {
            y_row[col] = yuyv_row[col * 2];
        }

        // Extract U and V values (subsampled)
        if (row % 2 == 0) {
            int uv_row = row / 2;
            uint8_t* u_row_out = u_out + uv_row * uv_stride;
            uint8_t* v_row_out = v_out + uv_row * uv_stride;

            for (int col = 0; col < width / 2; col++) {
                // Average U and V from two rows for better quality
                u_row_out[col] = yuyv_row[col * 4 + 1];
                v_row_out[col] = yuyv_row[col * 4 + 3];
            }
        }
    }
}

// NV12 to I420 conversion (deinterleave UV plane)
static void nv12_to_i420(const uint8_t* nv12_y, const uint8_t* nv12_uv,
                         int width, int height, int y_stride, int uv_stride,
                         uint8_t* y_out, uint8_t* u_out, uint8_t* v_out) {
    // Copy Y plane
    for (int row = 0; row < height; row++) {
        memcpy(y_out + row * width, nv12_y + row * y_stride, width);
    }

    // Deinterleave UV plane
    int uv_width = width / 2;
    int uv_height = height / 2;
    for (int row = 0; row < uv_height; row++) {
        const uint8_t* uv_row = nv12_uv + row * uv_stride;
        uint8_t* u_row = u_out + row * uv_width;
        uint8_t* v_row = v_out + row * uv_width;
        for (int col = 0; col < uv_width; col++) {
            u_row[col] = uv_row[col * 2];
            v_row[col] = uv_row[col * 2 + 1];
        }
    }
}

// Buffer structure for mmap
typedef struct {
    void* start;
    size_t length;
} Buffer;

// Capture context
typedef struct {
    int fd;
    int width;
    int height;
    int fps;
    uint32_t pixel_format;

    Buffer* buffers;
    int buffer_count;

    MediaV4L2FrameCallback callback;
    void* user_data;

    pthread_t capture_thread;
    volatile int running;

    // I420 conversion buffers
    uint8_t* y_buffer;
    uint8_t* u_buffer;
    uint8_t* v_buffer;
} CaptureContext;

// Device enumeration helpers
static int is_video_device(const char* path) {
    struct v4l2_capability cap;
    int fd = open(path, O_RDWR);
    if (fd < 0) return 0;

    int result = 0;
    if (ioctl(fd, VIDIOC_QUERYCAP, &cap) == 0) {
        // Check if it's a video capture device
        if ((cap.device_caps & V4L2_CAP_VIDEO_CAPTURE) &&
            (cap.device_caps & V4L2_CAP_STREAMING)) {
            result = 1;
        }
    }

    close(fd);
    return result;
}

int32_t media_v4l2_device_count(void) {
    int count = 0;

    // Scan /dev/video* devices
    for (int i = 0; i < 64; i++) {
        char path[32];
        snprintf(path, sizeof(path), "/dev/video%d", i);

        struct stat st;
        if (stat(path, &st) == 0 && is_video_device(path)) {
            count++;
        }
    }

    return count;
}

// Find the nth video capture device
static int find_device_index(int target_index, char* out_path, size_t path_size) {
    int found = 0;

    for (int i = 0; i < 64 && found <= target_index; i++) {
        char path[32];
        snprintf(path, sizeof(path), "/dev/video%d", i);

        struct stat st;
        if (stat(path, &st) == 0 && is_video_device(path)) {
            if (found == target_index) {
                strncpy(out_path, path, path_size - 1);
                out_path[path_size - 1] = '\0';
                return 0;
            }
            found++;
        }
    }

    return -1;
}

const char* media_v4l2_device_path(int32_t index) {
    char path[32];
    if (find_device_index(index, path, sizeof(path)) < 0) {
        return NULL;
    }
    return strdup(path);
}

const char* media_v4l2_device_name(int32_t index) {
    char path[32];
    if (find_device_index(index, path, sizeof(path)) < 0) {
        return NULL;
    }

    int fd = open(path, O_RDWR);
    if (fd < 0) {
        return strdup(path);
    }

    struct v4l2_capability cap;
    if (ioctl(fd, VIDIOC_QUERYCAP, &cap) < 0) {
        close(fd);
        return strdup(path);
    }

    close(fd);
    return strdup((const char*)cap.card);
}

MediaV4L2DeviceInfo* media_v4l2_get_device_info(int32_t index) {
    char path[32];
    if (find_device_index(index, path, sizeof(path)) < 0) {
        return NULL;
    }

    MediaV4L2DeviceInfo* info = malloc(sizeof(MediaV4L2DeviceInfo));
    if (!info) return NULL;

    info->device_path = strdup(path);
    info->index = index;

    int fd = open(path, O_RDWR);
    if (fd >= 0) {
        struct v4l2_capability cap;
        if (ioctl(fd, VIDIOC_QUERYCAP, &cap) == 0) {
            info->name = strdup((const char*)cap.card);
        } else {
            info->name = strdup(path);
        }
        close(fd);
    } else {
        info->name = strdup(path);
    }

    return info;
}

void media_v4l2_free_device_info(MediaV4L2DeviceInfo* info) {
    if (info) {
        free(info->device_path);
        free(info->name);
        free(info);
    }
}

void media_v4l2_free_string(const char* str) {
    if (str) {
        free((void*)str);
    }
}

// Capture thread function
static void* capture_thread_func(void* arg) {
    CaptureContext* ctx = (CaptureContext*)arg;

    while (ctx->running) {
        fd_set fds;
        FD_ZERO(&fds);
        FD_SET(ctx->fd, &fds);

        struct timeval tv;
        tv.tv_sec = 1;
        tv.tv_usec = 0;

        int r = select(ctx->fd + 1, &fds, NULL, NULL, &tv);
        if (r < 0) {
            if (errno == EINTR) continue;
            break;
        }
        if (r == 0) continue; // Timeout

        // Dequeue buffer
        struct v4l2_buffer buf;
        memset(&buf, 0, sizeof(buf));
        buf.type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
        buf.memory = V4L2_MEMORY_MMAP;

        if (ioctl(ctx->fd, VIDIOC_DQBUF, &buf) < 0) {
            if (errno == EAGAIN) continue;
            break;
        }

        // Get timestamp
        int64_t timestamp_ns = (int64_t)buf.timestamp.tv_sec * 1000000000LL +
                              (int64_t)buf.timestamp.tv_usec * 1000LL;

        // Convert to I420 if needed
        const uint8_t* y_plane = ctx->y_buffer;
        const uint8_t* u_plane = ctx->u_buffer;
        const uint8_t* v_plane = ctx->v_buffer;
        int y_stride = ctx->width;
        int uv_stride = ctx->width / 2;

        if (ctx->pixel_format == V4L2_PIX_FMT_YUYV) {
            yuyv_to_i420(ctx->buffers[buf.index].start,
                        ctx->width, ctx->height,
                        ctx->y_buffer, ctx->u_buffer, ctx->v_buffer);
        } else if (ctx->pixel_format == V4L2_PIX_FMT_NV12) {
            // NV12: Y plane followed by interleaved UV
            const uint8_t* nv12 = ctx->buffers[buf.index].start;
            nv12_to_i420(nv12, nv12 + ctx->width * ctx->height,
                        ctx->width, ctx->height, ctx->width, ctx->width,
                        ctx->y_buffer, ctx->u_buffer, ctx->v_buffer);
        } else if (ctx->pixel_format == V4L2_PIX_FMT_YUV420) {
            // Already I420, just copy
            const uint8_t* src = ctx->buffers[buf.index].start;
            int y_size = ctx->width * ctx->height;
            int uv_size = y_size / 4;
            memcpy(ctx->y_buffer, src, y_size);
            memcpy(ctx->u_buffer, src + y_size, uv_size);
            memcpy(ctx->v_buffer, src + y_size + uv_size, uv_size);
        }

        // Call callback
        if (ctx->callback) {
            ctx->callback(y_plane, y_stride,
                         u_plane, uv_stride,
                         v_plane, uv_stride,
                         ctx->width, ctx->height,
                         timestamp_ns, ctx->user_data);
        }

        // Requeue buffer
        if (ioctl(ctx->fd, VIDIOC_QBUF, &buf) < 0) {
            break;
        }
    }

    return NULL;
}

MediaV4L2Capture media_v4l2_capture_create(
    const char* device_path,
    int32_t width,
    int32_t height,
    int32_t fps,
    MediaV4L2FrameCallback callback,
    void* user_data
) {
    const char* path = device_path;
    char default_path[32];

    if (!path) {
        // Find first video capture device
        if (find_device_index(0, default_path, sizeof(default_path)) < 0) {
            set_error("No video capture devices found");
            return 0;
        }
        path = default_path;
    }

    // Open device
    int fd = open(path, O_RDWR | O_NONBLOCK);
    if (fd < 0) {
        set_error("Cannot open %s: %s", path, strerror(errno));
        return 0;
    }

    // Check capabilities
    struct v4l2_capability cap;
    if (ioctl(fd, VIDIOC_QUERYCAP, &cap) < 0) {
        set_error("VIDIOC_QUERYCAP failed: %s", strerror(errno));
        close(fd);
        return 0;
    }

    if (!(cap.device_caps & V4L2_CAP_VIDEO_CAPTURE)) {
        set_error("Device does not support video capture");
        close(fd);
        return 0;
    }

    if (!(cap.device_caps & V4L2_CAP_STREAMING)) {
        set_error("Device does not support streaming I/O");
        close(fd);
        return 0;
    }

    // Set format - try multiple formats in order of preference
    struct v4l2_format fmt;
    memset(&fmt, 0, sizeof(fmt));
    fmt.type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
    fmt.fmt.pix.width = width > 0 ? width : 640;
    fmt.fmt.pix.height = height > 0 ? height : 480;
    fmt.fmt.pix.field = V4L2_FIELD_NONE;

    // Try formats in order of preference: YUV420 (I420), NV12, YUYV
    uint32_t formats[] = {V4L2_PIX_FMT_YUV420, V4L2_PIX_FMT_NV12, V4L2_PIX_FMT_YUYV};
    int format_found = 0;

    for (int i = 0; i < 3; i++) {
        fmt.fmt.pix.pixelformat = formats[i];
        if (ioctl(fd, VIDIOC_S_FMT, &fmt) == 0) {
            format_found = 1;
            break;
        }
    }

    if (!format_found) {
        set_error("Failed to set video format");
        close(fd);
        return 0;
    }

    // Actual dimensions may differ from requested
    int actual_width = fmt.fmt.pix.width;
    int actual_height = fmt.fmt.pix.height;
    uint32_t pixel_format = fmt.fmt.pix.pixelformat;

    // Set frame rate
    struct v4l2_streamparm parm;
    memset(&parm, 0, sizeof(parm));
    parm.type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
    parm.parm.capture.timeperframe.numerator = 1;
    parm.parm.capture.timeperframe.denominator = fps > 0 ? fps : 30;
    ioctl(fd, VIDIOC_S_PARM, &parm);  // May fail, that's okay

    // Request buffers
    struct v4l2_requestbuffers req;
    memset(&req, 0, sizeof(req));
    req.count = 4;
    req.type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
    req.memory = V4L2_MEMORY_MMAP;

    if (ioctl(fd, VIDIOC_REQBUFS, &req) < 0) {
        set_error("VIDIOC_REQBUFS failed: %s", strerror(errno));
        close(fd);
        return 0;
    }

    if (req.count < 2) {
        set_error("Insufficient buffer memory");
        close(fd);
        return 0;
    }

    // Allocate context
    CaptureContext* ctx = calloc(1, sizeof(CaptureContext));
    if (!ctx) {
        set_error("Out of memory");
        close(fd);
        return 0;
    }

    ctx->fd = fd;
    ctx->width = actual_width;
    ctx->height = actual_height;
    ctx->fps = fps > 0 ? fps : 30;
    ctx->pixel_format = pixel_format;
    ctx->callback = callback;
    ctx->user_data = user_data;
    ctx->buffer_count = req.count;

    // Allocate I420 conversion buffers
    int y_size = actual_width * actual_height;
    int uv_size = y_size / 4;
    ctx->y_buffer = malloc(y_size);
    ctx->u_buffer = malloc(uv_size);
    ctx->v_buffer = malloc(uv_size);

    if (!ctx->y_buffer || !ctx->u_buffer || !ctx->v_buffer) {
        set_error("Failed to allocate conversion buffers");
        free(ctx->y_buffer);
        free(ctx->u_buffer);
        free(ctx->v_buffer);
        free(ctx);
        close(fd);
        return 0;
    }

    // Map buffers
    ctx->buffers = calloc(req.count, sizeof(Buffer));
    if (!ctx->buffers) {
        set_error("Out of memory");
        free(ctx->y_buffer);
        free(ctx->u_buffer);
        free(ctx->v_buffer);
        free(ctx);
        close(fd);
        return 0;
    }

    for (unsigned int i = 0; i < req.count; i++) {
        struct v4l2_buffer buf;
        memset(&buf, 0, sizeof(buf));
        buf.type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
        buf.memory = V4L2_MEMORY_MMAP;
        buf.index = i;

        if (ioctl(fd, VIDIOC_QUERYBUF, &buf) < 0) {
            set_error("VIDIOC_QUERYBUF failed: %s", strerror(errno));
            // Cleanup mapped buffers
            for (unsigned int j = 0; j < i; j++) {
                munmap(ctx->buffers[j].start, ctx->buffers[j].length);
            }
            free(ctx->buffers);
            free(ctx->y_buffer);
            free(ctx->u_buffer);
            free(ctx->v_buffer);
            free(ctx);
            close(fd);
            return 0;
        }

        ctx->buffers[i].length = buf.length;
        ctx->buffers[i].start = mmap(NULL, buf.length,
                                     PROT_READ | PROT_WRITE,
                                     MAP_SHARED, fd, buf.m.offset);

        if (ctx->buffers[i].start == MAP_FAILED) {
            set_error("mmap failed: %s", strerror(errno));
            for (unsigned int j = 0; j < i; j++) {
                munmap(ctx->buffers[j].start, ctx->buffers[j].length);
            }
            free(ctx->buffers);
            free(ctx->y_buffer);
            free(ctx->u_buffer);
            free(ctx->v_buffer);
            free(ctx);
            close(fd);
            return 0;
        }
    }

    return (MediaV4L2Capture)ctx;
}

int32_t media_v4l2_capture_start(MediaV4L2Capture handle) {
    if (!handle) return MEDIA_V4L2_ERROR;

    CaptureContext* ctx = (CaptureContext*)handle;

    if (ctx->running) return MEDIA_V4L2_OK;

    // Queue all buffers
    for (int i = 0; i < ctx->buffer_count; i++) {
        struct v4l2_buffer buf;
        memset(&buf, 0, sizeof(buf));
        buf.type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
        buf.memory = V4L2_MEMORY_MMAP;
        buf.index = i;

        if (ioctl(ctx->fd, VIDIOC_QBUF, &buf) < 0) {
            set_error("VIDIOC_QBUF failed: %s", strerror(errno));
            return MEDIA_V4L2_ERROR;
        }
    }

    // Start streaming
    enum v4l2_buf_type type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
    if (ioctl(ctx->fd, VIDIOC_STREAMON, &type) < 0) {
        set_error("VIDIOC_STREAMON failed: %s", strerror(errno));
        return MEDIA_V4L2_ERROR;
    }

    // Start capture thread
    ctx->running = 1;
    if (pthread_create(&ctx->capture_thread, NULL, capture_thread_func, ctx) != 0) {
        set_error("Failed to create capture thread");
        ctx->running = 0;
        ioctl(ctx->fd, VIDIOC_STREAMOFF, &type);
        return MEDIA_V4L2_ERROR;
    }

    return MEDIA_V4L2_OK;
}

int32_t media_v4l2_capture_stop(MediaV4L2Capture handle) {
    if (!handle) return MEDIA_V4L2_ERROR;

    CaptureContext* ctx = (CaptureContext*)handle;

    if (!ctx->running) return MEDIA_V4L2_OK;

    // Stop capture thread
    ctx->running = 0;
    pthread_join(ctx->capture_thread, NULL);

    // Stop streaming
    enum v4l2_buf_type type = V4L2_BUF_TYPE_VIDEO_CAPTURE;
    ioctl(ctx->fd, VIDIOC_STREAMOFF, &type);

    return MEDIA_V4L2_OK;
}

void media_v4l2_capture_destroy(MediaV4L2Capture handle) {
    if (!handle) return;

    CaptureContext* ctx = (CaptureContext*)handle;

    // Stop if running
    media_v4l2_capture_stop(handle);

    // Unmap buffers
    for (int i = 0; i < ctx->buffer_count; i++) {
        if (ctx->buffers[i].start && ctx->buffers[i].start != MAP_FAILED) {
            munmap(ctx->buffers[i].start, ctx->buffers[i].length);
        }
    }

    free(ctx->buffers);
    free(ctx->y_buffer);
    free(ctx->u_buffer);
    free(ctx->v_buffer);
    close(ctx->fd);
    free(ctx);
}

int32_t media_v4l2_capture_get_width(MediaV4L2Capture handle) {
    if (!handle) return 0;
    return ((CaptureContext*)handle)->width;
}

int32_t media_v4l2_capture_get_height(MediaV4L2Capture handle) {
    if (!handle) return 0;
    return ((CaptureContext*)handle)->height;
}

int32_t media_v4l2_capture_get_fps(MediaV4L2Capture handle) {
    if (!handle) return 0;
    return ((CaptureContext*)handle)->fps;
}
