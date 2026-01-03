// stream_alsa.c - ALSA wrapper implementation for Linux
//
// Compile with:
//   cc -shared -fPIC -O2 -o libstream_alsa.so stream_alsa.c -lasound -lpthread

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <pthread.h>
#include <stdarg.h>
#include <time.h>
#include <alsa/asoundlib.h>

#include "stream_alsa.h"

// Thread-local error buffer
static __thread char error_buffer[256] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(error_buffer, sizeof(error_buffer), fmt, args);
    va_end(args);
}

const char* stream_alsa_get_error(void) {
    return error_buffer;
}

// Device info cache
typedef struct DeviceEntry {
    char* hw_id;
    char* name;
    int card;
    int device;
} DeviceEntry;

static DeviceEntry* cached_devices = NULL;
static int cached_device_count = 0;
static pthread_mutex_t cache_mutex = PTHREAD_MUTEX_INITIALIZER;

static void clear_device_cache(void) {
    for (int i = 0; i < cached_device_count; i++) {
        free(cached_devices[i].hw_id);
        free(cached_devices[i].name);
    }
    free(cached_devices);
    cached_devices = NULL;
    cached_device_count = 0;
}

static void enumerate_devices(void) {
    pthread_mutex_lock(&cache_mutex);

    // Clear existing cache
    clear_device_cache();

    // Count devices first
    int count = 0;
    int card = -1;

    while (snd_card_next(&card) >= 0 && card >= 0) {
        snd_ctl_t* ctl;
        char name[32];
        snprintf(name, sizeof(name), "hw:%d", card);

        if (snd_ctl_open(&ctl, name, 0) < 0) continue;

        int device = -1;
        while (snd_ctl_pcm_next_device(ctl, &device) >= 0 && device >= 0) {
            snd_pcm_info_t* pcm_info;
            snd_pcm_info_alloca(&pcm_info);
            snd_pcm_info_set_device(pcm_info, device);
            snd_pcm_info_set_subdevice(pcm_info, 0);
            snd_pcm_info_set_stream(pcm_info, SND_PCM_STREAM_CAPTURE);

            if (snd_ctl_pcm_info(ctl, pcm_info) >= 0) {
                count++;
            }
        }

        snd_ctl_close(ctl);
    }

    if (count == 0) {
        pthread_mutex_unlock(&cache_mutex);
        return;
    }

    // Allocate and fill device list
    cached_devices = calloc(count, sizeof(DeviceEntry));
    if (!cached_devices) {
        pthread_mutex_unlock(&cache_mutex);
        return;
    }

    int idx = 0;
    card = -1;

    while (snd_card_next(&card) >= 0 && card >= 0 && idx < count) {
        snd_ctl_t* ctl;
        char hw_name[32];
        snprintf(hw_name, sizeof(hw_name), "hw:%d", card);

        if (snd_ctl_open(&ctl, hw_name, 0) < 0) continue;

        // Get card info
        snd_ctl_card_info_t* card_info;
        snd_ctl_card_info_alloca(&card_info);
        snd_ctl_card_info(ctl, card_info);

        int device = -1;
        while (snd_ctl_pcm_next_device(ctl, &device) >= 0 && device >= 0 && idx < count) {
            snd_pcm_info_t* pcm_info;
            snd_pcm_info_alloca(&pcm_info);
            snd_pcm_info_set_device(pcm_info, device);
            snd_pcm_info_set_subdevice(pcm_info, 0);
            snd_pcm_info_set_stream(pcm_info, SND_PCM_STREAM_CAPTURE);

            if (snd_ctl_pcm_info(ctl, pcm_info) >= 0) {
                char hw_id[64];
                snprintf(hw_id, sizeof(hw_id), "plughw:%d,%d", card, device);

                char dev_name[256];
                const char* card_name = snd_ctl_card_info_get_name(card_info);
                const char* pcm_name = snd_pcm_info_get_name(pcm_info);
                snprintf(dev_name, sizeof(dev_name), "%s: %s", card_name, pcm_name);

                cached_devices[idx].hw_id = strdup(hw_id);
                cached_devices[idx].name = strdup(dev_name);
                cached_devices[idx].card = card;
                cached_devices[idx].device = device;
                idx++;
            }
        }

        snd_ctl_close(ctl);
    }

    cached_device_count = idx;
    pthread_mutex_unlock(&cache_mutex);
}

int32_t stream_alsa_input_device_count(void) {
    enumerate_devices();
    return cached_device_count;
}

const char* stream_alsa_input_device_id(int32_t index) {
    pthread_mutex_lock(&cache_mutex);
    if (index < 0 || index >= cached_device_count) {
        pthread_mutex_unlock(&cache_mutex);
        return NULL;
    }
    char* result = strdup(cached_devices[index].hw_id);
    pthread_mutex_unlock(&cache_mutex);
    return result;
}

const char* stream_alsa_input_device_name(int32_t index) {
    pthread_mutex_lock(&cache_mutex);
    if (index < 0 || index >= cached_device_count) {
        pthread_mutex_unlock(&cache_mutex);
        return NULL;
    }
    char* result = strdup(cached_devices[index].name);
    pthread_mutex_unlock(&cache_mutex);
    return result;
}

StreamALSADeviceInfo* stream_alsa_get_input_device_info(int32_t index) {
    pthread_mutex_lock(&cache_mutex);
    if (index < 0 || index >= cached_device_count) {
        pthread_mutex_unlock(&cache_mutex);
        return NULL;
    }

    StreamALSADeviceInfo* info = malloc(sizeof(StreamALSADeviceInfo));
    if (!info) {
        pthread_mutex_unlock(&cache_mutex);
        return NULL;
    }

    info->hw_id = strdup(cached_devices[index].hw_id);
    info->name = strdup(cached_devices[index].name);
    info->card = cached_devices[index].card;
    info->device = cached_devices[index].device;

    pthread_mutex_unlock(&cache_mutex);
    return info;
}

void stream_alsa_free_device_info(StreamALSADeviceInfo* info) {
    if (info) {
        free(info->hw_id);
        free(info->name);
        free(info);
    }
}

void stream_alsa_free_string(const char* str) {
    if (str) {
        free((void*)str);
    }
}

// Capture context
typedef struct {
    snd_pcm_t* pcm;
    int sample_rate;
    int channels;
    snd_pcm_uframes_t period_size;

    StreamALSAAudioCallback callback;
    void* user_data;

    pthread_t capture_thread;
    volatile int running;

    int16_t* buffer;
    size_t buffer_frames;
} CaptureContext;

// Capture thread function
static void* capture_thread_func(void* arg) {
    CaptureContext* ctx = (CaptureContext*)arg;
    struct timespec ts;

    while (ctx->running) {
        // Read audio data
        snd_pcm_sframes_t frames = snd_pcm_readi(ctx->pcm, ctx->buffer, ctx->buffer_frames);

        if (frames < 0) {
            // Handle xrun (buffer overrun)
            if (frames == -EPIPE) {
                snd_pcm_prepare(ctx->pcm);
                continue;
            }
            // Other errors
            frames = snd_pcm_recover(ctx->pcm, frames, 0);
            if (frames < 0) {
                break;
            }
            continue;
        }

        if (frames == 0) continue;

        // Get timestamp
        clock_gettime(CLOCK_MONOTONIC, &ts);
        int64_t timestamp_ns = (int64_t)ts.tv_sec * 1000000000LL + ts.tv_nsec;

        // Call callback
        if (ctx->callback) {
            ctx->callback(ctx->buffer, frames, ctx->channels,
                         ctx->sample_rate, timestamp_ns, ctx->user_data);
        }
    }

    return NULL;
}

StreamALSACapture stream_alsa_capture_create(
    const char* device_id,
    int32_t sample_rate,
    int32_t channels,
    StreamALSAAudioCallback callback,
    void* user_data
) {
    const char* device = device_id ? device_id : "default";
    int err;

    // Open PCM device for capture
    snd_pcm_t* pcm;
    err = snd_pcm_open(&pcm, device, SND_PCM_STREAM_CAPTURE, 0);
    if (err < 0) {
        set_error("Cannot open audio device %s: %s", device, snd_strerror(err));
        return 0;
    }

    // Set hardware parameters
    snd_pcm_hw_params_t* hw_params;
    snd_pcm_hw_params_alloca(&hw_params);
    snd_pcm_hw_params_any(pcm, hw_params);

    // Set access type (interleaved)
    err = snd_pcm_hw_params_set_access(pcm, hw_params, SND_PCM_ACCESS_RW_INTERLEAVED);
    if (err < 0) {
        set_error("Cannot set access type: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Set format (16-bit signed little-endian)
    err = snd_pcm_hw_params_set_format(pcm, hw_params, SND_PCM_FORMAT_S16_LE);
    if (err < 0) {
        set_error("Cannot set sample format: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Set channels
    unsigned int actual_channels = channels > 0 ? channels : 1;
    err = snd_pcm_hw_params_set_channels_near(pcm, hw_params, &actual_channels);
    if (err < 0) {
        set_error("Cannot set channels: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Set sample rate
    unsigned int actual_rate = sample_rate > 0 ? sample_rate : 48000;
    err = snd_pcm_hw_params_set_rate_near(pcm, hw_params, &actual_rate, NULL);
    if (err < 0) {
        set_error("Cannot set sample rate: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Set period size (buffer size per read) - ~20ms
    snd_pcm_uframes_t period_size = actual_rate / 50;  // 20ms
    err = snd_pcm_hw_params_set_period_size_near(pcm, hw_params, &period_size, NULL);
    if (err < 0) {
        set_error("Cannot set period size: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Set buffer size (multiple periods)
    snd_pcm_uframes_t buffer_size = period_size * 4;
    err = snd_pcm_hw_params_set_buffer_size_near(pcm, hw_params, &buffer_size);
    if (err < 0) {
        set_error("Cannot set buffer size: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Apply hardware parameters
    err = snd_pcm_hw_params(pcm, hw_params);
    if (err < 0) {
        set_error("Cannot set hardware parameters: %s", snd_strerror(err));
        snd_pcm_close(pcm);
        return 0;
    }

    // Get actual period size
    snd_pcm_hw_params_get_period_size(hw_params, &period_size, NULL);

    // Allocate context
    CaptureContext* ctx = calloc(1, sizeof(CaptureContext));
    if (!ctx) {
        set_error("Out of memory");
        snd_pcm_close(pcm);
        return 0;
    }

    ctx->pcm = pcm;
    ctx->sample_rate = actual_rate;
    ctx->channels = actual_channels;
    ctx->period_size = period_size;
    ctx->callback = callback;
    ctx->user_data = user_data;
    ctx->buffer_frames = period_size;

    // Allocate sample buffer
    ctx->buffer = malloc(period_size * actual_channels * sizeof(int16_t));
    if (!ctx->buffer) {
        set_error("Out of memory");
        snd_pcm_close(pcm);
        free(ctx);
        return 0;
    }

    return (StreamALSACapture)ctx;
}

int32_t stream_alsa_capture_start(StreamALSACapture handle) {
    if (!handle) return STREAM_ALSA_ERROR;

    CaptureContext* ctx = (CaptureContext*)handle;

    if (ctx->running) return STREAM_ALSA_OK;

    // Prepare PCM
    int err = snd_pcm_prepare(ctx->pcm);
    if (err < 0) {
        set_error("Cannot prepare audio device: %s", snd_strerror(err));
        return STREAM_ALSA_ERROR;
    }

    // Start capture thread
    ctx->running = 1;
    if (pthread_create(&ctx->capture_thread, NULL, capture_thread_func, ctx) != 0) {
        set_error("Failed to create capture thread");
        ctx->running = 0;
        return STREAM_ALSA_ERROR;
    }

    return STREAM_ALSA_OK;
}

int32_t stream_alsa_capture_stop(StreamALSACapture handle) {
    if (!handle) return STREAM_ALSA_ERROR;

    CaptureContext* ctx = (CaptureContext*)handle;

    if (!ctx->running) return STREAM_ALSA_OK;

    // Stop capture thread
    ctx->running = 0;
    snd_pcm_drop(ctx->pcm);  // Wake up blocking read
    pthread_join(ctx->capture_thread, NULL);

    return STREAM_ALSA_OK;
}

void stream_alsa_capture_destroy(StreamALSACapture handle) {
    if (!handle) return;

    CaptureContext* ctx = (CaptureContext*)handle;

    // Stop if running
    stream_alsa_capture_stop(handle);

    // Close PCM
    snd_pcm_close(ctx->pcm);

    // Free resources
    free(ctx->buffer);
    free(ctx);
}

int32_t stream_alsa_capture_get_sample_rate(StreamALSACapture handle) {
    if (!handle) return 0;
    return ((CaptureContext*)handle)->sample_rate;
}

int32_t stream_alsa_capture_get_channels(StreamALSACapture handle) {
    if (!handle) return 0;
    return ((CaptureContext*)handle)->channels;
}
