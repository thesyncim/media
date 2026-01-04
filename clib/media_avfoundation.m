// media_avfoundation.m - AVFoundation wrapper implementation
//
// Compile with:
//   clang -shared -fPIC -O2 -fobjc-arc -framework AVFoundation -framework CoreMedia \
//         -framework CoreVideo -framework Foundation \
//         -o libmedia_avfoundation.dylib media_avfoundation.m

#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <Foundation/Foundation.h>
#include <pthread.h>
#include "media_avfoundation.h"

static __thread char error_buffer[256] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(error_buffer, sizeof(error_buffer), fmt, args);
    va_end(args);
}

const char* media_av_get_error(void) {
    return error_buffer;
}

// Helper to get device types based on availability
static NSArray<AVCaptureDeviceType>* getVideoDeviceTypes(void) {
    NSMutableArray* types = [NSMutableArray array];
    [types addObject:AVCaptureDeviceTypeBuiltInWideAngleCamera];
    if (@available(macOS 14.0, *)) {
        [types addObject:AVCaptureDeviceTypeExternal];
    } else {
        #pragma clang diagnostic push
        #pragma clang diagnostic ignored "-Wdeprecated-declarations"
        [types addObject:AVCaptureDeviceTypeExternalUnknown];
        #pragma clang diagnostic pop
    }
    return types;
}

static NSArray<AVCaptureDeviceType>* getAudioDeviceTypes(void) {
    NSMutableArray* types = [NSMutableArray array];
    if (@available(macOS 14.0, *)) {
        [types addObject:AVCaptureDeviceTypeMicrophone];
        [types addObject:AVCaptureDeviceTypeExternal];
    } else {
        #pragma clang diagnostic push
        #pragma clang diagnostic ignored "-Wdeprecated-declarations"
        [types addObject:AVCaptureDeviceTypeBuiltInMicrophone];
        [types addObject:AVCaptureDeviceTypeExternalUnknown];
        #pragma clang diagnostic pop
    }
    return types;
}

// Device enumeration
int32_t media_av_video_device_count(void) {
    @autoreleasepool {
        if (@available(macOS 10.15, *)) {
            AVCaptureDeviceDiscoverySession* session = [AVCaptureDeviceDiscoverySession
                discoverySessionWithDeviceTypes:getVideoDeviceTypes()
                mediaType:AVMediaTypeVideo
                position:AVCaptureDevicePositionUnspecified];
            return (int32_t)session.devices.count;
        }
        return 0;
    }
}

int32_t media_av_audio_input_device_count(void) {
    @autoreleasepool {
        if (@available(macOS 10.15, *)) {
            AVCaptureDeviceDiscoverySession* session = [AVCaptureDeviceDiscoverySession
                discoverySessionWithDeviceTypes:getAudioDeviceTypes()
                mediaType:AVMediaTypeAudio
                position:AVCaptureDevicePositionUnspecified];
            return (int32_t)session.devices.count;
        }
        return 0;
    }
}

const char* media_av_video_device_id(int32_t index) {
    @autoreleasepool {
        if (@available(macOS 10.15, *)) {
            AVCaptureDeviceDiscoverySession* session = [AVCaptureDeviceDiscoverySession
                discoverySessionWithDeviceTypes:getVideoDeviceTypes()
                mediaType:AVMediaTypeVideo
                position:AVCaptureDevicePositionUnspecified];
            if (index >= 0 && index < (int32_t)session.devices.count) {
                AVCaptureDevice* device = session.devices[index];
                return strdup([device.uniqueID UTF8String]);
            }
        }
        return NULL;
    }
}

const char* media_av_video_device_label(int32_t index) {
    @autoreleasepool {
        if (@available(macOS 10.15, *)) {
            AVCaptureDeviceDiscoverySession* session = [AVCaptureDeviceDiscoverySession
                discoverySessionWithDeviceTypes:getVideoDeviceTypes()
                mediaType:AVMediaTypeVideo
                position:AVCaptureDevicePositionUnspecified];
            if (index >= 0 && index < (int32_t)session.devices.count) {
                AVCaptureDevice* device = session.devices[index];
                return strdup([device.localizedName UTF8String]);
            }
        }
        return NULL;
    }
}

const char* media_av_audio_input_device_id(int32_t index) {
    @autoreleasepool {
        if (@available(macOS 10.15, *)) {
            AVCaptureDeviceDiscoverySession* session = [AVCaptureDeviceDiscoverySession
                discoverySessionWithDeviceTypes:getAudioDeviceTypes()
                mediaType:AVMediaTypeAudio
                position:AVCaptureDevicePositionUnspecified];
            if (index >= 0 && index < (int32_t)session.devices.count) {
                AVCaptureDevice* device = session.devices[index];
                return strdup([device.uniqueID UTF8String]);
            }
        }
        return NULL;
    }
}

const char* media_av_audio_input_device_label(int32_t index) {
    @autoreleasepool {
        if (@available(macOS 10.15, *)) {
            AVCaptureDeviceDiscoverySession* session = [AVCaptureDeviceDiscoverySession
                discoverySessionWithDeviceTypes:getAudioDeviceTypes()
                mediaType:AVMediaTypeAudio
                position:AVCaptureDevicePositionUnspecified];
            if (index >= 0 && index < (int32_t)session.devices.count) {
                AVCaptureDevice* device = session.devices[index];
                return strdup([device.localizedName UTF8String]);
            }
        }
        return NULL;
    }
}

void media_av_free_string(const char* str) {
    if (str) {
        free((void*)str);
    }
}

// Helper to find device by ID or get default
static AVCaptureDevice* findVideoDevice(const char* device_id) {
    NSString* deviceIdStr = device_id ? [NSString stringWithUTF8String:device_id] : nil;
    AVCaptureDevice* device = nil;

    if (deviceIdStr) {
        device = [AVCaptureDevice deviceWithUniqueID:deviceIdStr];
    } else {
        device = [AVCaptureDevice defaultDeviceWithMediaType:AVMediaTypeVideo];
    }
    return device;
}

int32_t media_av_video_device_fps_range(const char* device_id, int32_t* min_fps, int32_t* max_fps) {
    @autoreleasepool {
        AVCaptureDevice* device = findVideoDevice(device_id);
        if (!device) {
            set_error("Video device not found");
            return MEDIA_AV_ERROR_NOTFOUND;
        }

        // Get the active format's supported frame rate ranges
        AVCaptureDeviceFormat* format = device.activeFormat;
        if (!format || format.videoSupportedFrameRateRanges.count == 0) {
            set_error("No supported frame rate ranges");
            return MEDIA_AV_ERROR;
        }

        // Find the overall min and max across all ranges
        Float64 overallMin = DBL_MAX;
        Float64 overallMax = 0;

        for (AVFrameRateRange* range in format.videoSupportedFrameRateRanges) {
            if (range.minFrameRate < overallMin) overallMin = range.minFrameRate;
            if (range.maxFrameRate > overallMax) overallMax = range.maxFrameRate;
        }

        if (min_fps) *min_fps = (int32_t)overallMin;
        if (max_fps) *max_fps = (int32_t)overallMax;

        return MEDIA_AV_OK;
    }
}

// Permission handling
int32_t media_av_camera_permission_status(void) {
    @autoreleasepool {
        if (@available(macOS 10.14, *)) {
            return (int32_t)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
        }
        return MEDIA_AV_AUTH_AUTHORIZED;
    }
}

int32_t media_av_microphone_permission_status(void) {
    @autoreleasepool {
        if (@available(macOS 10.14, *)) {
            return (int32_t)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
        }
        return MEDIA_AV_AUTH_AUTHORIZED;
    }
}

void media_av_request_camera_permission(void) {
    @autoreleasepool {
        if (@available(macOS 10.14, *)) {
            [AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL granted) {}];
        }
    }
}

void media_av_request_microphone_permission(void) {
    @autoreleasepool {
        if (@available(macOS 10.14, *)) {
            [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL granted) {}];
        }
    }
}

// Video capture delegate
@interface MediaVideoDelegate : NSObject <AVCaptureVideoDataOutputSampleBufferDelegate>
@property (nonatomic) MediaAVFrameCallback callback;
@property (nonatomic) void* userData;
@property (nonatomic) int32_t width;
@property (nonatomic) int32_t height;
@property (nonatomic, strong) NSMutableData* uBuffer;
@property (nonatomic, strong) NSMutableData* vBuffer;
@end

@implementation MediaVideoDelegate

- (void)captureOutput:(AVCaptureOutput *)output
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
    fromConnection:(AVCaptureConnection *)connection {

    if (!self.callback) return;

    CVImageBufferRef imageBuffer = CMSampleBufferGetImageBuffer(sampleBuffer);
    if (!imageBuffer) return;

    CVPixelBufferLockBaseAddress(imageBuffer, kCVPixelBufferLock_ReadOnly);

    OSType pixelFormat = CVPixelBufferGetPixelFormatType(imageBuffer);
    size_t width = CVPixelBufferGetWidth(imageBuffer);
    size_t height = CVPixelBufferGetHeight(imageBuffer);

    CMTime presentationTime = CMSampleBufferGetPresentationTimeStamp(sampleBuffer);
    int64_t timestamp_ns = (int64_t)(CMTimeGetSeconds(presentationTime) * 1e9);

    // Handle NV12 format (most common from cameras)
    if (pixelFormat == kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange ||
        pixelFormat == kCVPixelFormatType_420YpCbCr8BiPlanarFullRange) {

        uint8_t* yPlane = CVPixelBufferGetBaseAddressOfPlane(imageBuffer, 0);
        uint8_t* uvPlane = CVPixelBufferGetBaseAddressOfPlane(imageBuffer, 1);
        size_t yStride = CVPixelBufferGetBytesPerRowOfPlane(imageBuffer, 0);
        size_t uvStride = CVPixelBufferGetBytesPerRowOfPlane(imageBuffer, 1);

        // Allocate conversion buffers if needed
        size_t uvWidth = width / 2;
        size_t uvHeight = height / 2;
        size_t uvSize = uvWidth * uvHeight;

        if (!self.uBuffer || self.uBuffer.length < uvSize) {
            self.uBuffer = [NSMutableData dataWithLength:uvSize];
        }
        if (!self.vBuffer || self.vBuffer.length < uvSize) {
            self.vBuffer = [NSMutableData dataWithLength:uvSize];
        }

        uint8_t* uPlaneOut = (uint8_t*)self.uBuffer.mutableBytes;
        uint8_t* vPlaneOut = (uint8_t*)self.vBuffer.mutableBytes;

        // Deinterleave UV to U and V planes
        for (size_t y = 0; y < uvHeight; y++) {
            const uint8_t* uvRow = uvPlane + y * uvStride;
            uint8_t* uRow = uPlaneOut + y * uvWidth;
            uint8_t* vRow = vPlaneOut + y * uvWidth;
            for (size_t x = 0; x < uvWidth; x++) {
                uRow[x] = uvRow[x * 2];
                vRow[x] = uvRow[x * 2 + 1];
            }
        }

        self.callback(yPlane, (int32_t)yStride,
                     uPlaneOut, (int32_t)uvWidth,
                     vPlaneOut, (int32_t)uvWidth,
                     (int32_t)width, (int32_t)height,
                     timestamp_ns, self.userData);
    }

    CVPixelBufferUnlockBaseAddress(imageBuffer, kCVPixelBufferLock_ReadOnly);
}

@end

// Video capture wrapper - stores ObjC objects using CFRetain/CFRelease
@interface MediaVideoCaptureWrapper : NSObject
@property (nonatomic, strong) AVCaptureSession* session;
@property (nonatomic, strong) AVCaptureDeviceInput* input;
@property (nonatomic, strong) AVCaptureVideoDataOutput* output;
@property (nonatomic, strong) MediaVideoDelegate* delegate;
@property (nonatomic, strong) dispatch_queue_t queue;
@end

@implementation MediaVideoCaptureWrapper
@end

MediaAVVideoCapture media_av_video_capture_create(
    const char* device_id,
    int32_t width,
    int32_t height,
    int32_t fps,
    MediaAVFrameCallback callback,
    void* user_data
) {
    @autoreleasepool {
        // Check permission
        if (media_av_camera_permission_status() != MEDIA_AV_AUTH_AUTHORIZED) {
            set_error("Camera permission not granted");
            return 0;
        }

        // Find device
        AVCaptureDevice* device = findVideoDevice(device_id);
        if (!device) {
            set_error("Video device not found");
            return 0;
        }

        NSError* error = nil;

        // Create wrapper to hold objects
        MediaVideoCaptureWrapper* wrapper = [[MediaVideoCaptureWrapper alloc] init];

        // Create session
        wrapper.session = [[AVCaptureSession alloc] init];

        // Set session preset based on requested resolution
        if (width >= 1920 && height >= 1080) {
            wrapper.session.sessionPreset = AVCaptureSessionPreset1920x1080;
        } else if (width >= 1280 && height >= 720) {
            wrapper.session.sessionPreset = AVCaptureSessionPreset1280x720;
        } else if (width >= 640 && height >= 480) {
            wrapper.session.sessionPreset = AVCaptureSessionPreset640x480;
        } else {
            wrapper.session.sessionPreset = AVCaptureSessionPresetMedium;
        }

        // Create input
        wrapper.input = [AVCaptureDeviceInput deviceInputWithDevice:device error:&error];
        if (!wrapper.input) {
            set_error("Failed to create device input: %s", [[error localizedDescription] UTF8String]);
            return 0;
        }

        if (![wrapper.session canAddInput:wrapper.input]) {
            set_error("Cannot add input to session");
            return 0;
        }
        [wrapper.session addInput:wrapper.input];

        // Clamp FPS to device's supported range to avoid crashes
        int32_t actualFps = fps > 0 ? fps : 30;
        AVCaptureDeviceFormat* format = device.activeFormat;
        if (format && format.videoSupportedFrameRateRanges.count > 0) {
            Float64 maxSupported = 0;
            for (AVFrameRateRange* range in format.videoSupportedFrameRateRanges) {
                if (range.maxFrameRate > maxSupported) {
                    maxSupported = range.maxFrameRate;
                }
            }
            if (actualFps > (int32_t)maxSupported) {
                actualFps = (int32_t)maxSupported;
            }
        }

        // Configure device for (clamped) framerate
        if ([device lockForConfiguration:&error]) {
            CMTime frameDuration = CMTimeMake(1, actualFps);
            device.activeVideoMinFrameDuration = frameDuration;
            device.activeVideoMaxFrameDuration = frameDuration;
            [device unlockForConfiguration];
        }

        // Create output
        wrapper.output = [[AVCaptureVideoDataOutput alloc] init];
        wrapper.output.videoSettings = @{
            (NSString*)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange)
        };
        wrapper.output.alwaysDiscardsLateVideoFrames = YES;

        // Create delegate
        wrapper.delegate = [[MediaVideoDelegate alloc] init];
        wrapper.delegate.callback = callback;
        wrapper.delegate.userData = user_data;
        wrapper.delegate.width = width;
        wrapper.delegate.height = height;

        wrapper.queue = dispatch_queue_create("stream.video.capture", DISPATCH_QUEUE_SERIAL);
        [wrapper.output setSampleBufferDelegate:wrapper.delegate queue:wrapper.queue];

        if (![wrapper.session canAddOutput:wrapper.output]) {
            set_error("Cannot add output to session");
            return 0;
        }
        [wrapper.session addOutput:wrapper.output];

        // Return retained wrapper as handle
        return (MediaAVVideoCapture)CFBridgingRetain(wrapper);
    }
}

int32_t media_av_video_capture_start(MediaAVVideoCapture handle) {
    if (!handle) return MEDIA_AV_ERROR;

    @autoreleasepool {
        MediaVideoCaptureWrapper* wrapper = (__bridge MediaVideoCaptureWrapper*)(void*)handle;
        if (!wrapper.session.isRunning) {
            [wrapper.session startRunning];
        }
        return MEDIA_AV_OK;
    }
}

int32_t media_av_video_capture_stop(MediaAVVideoCapture handle) {
    if (!handle) return MEDIA_AV_ERROR;

    @autoreleasepool {
        MediaVideoCaptureWrapper* wrapper = (__bridge MediaVideoCaptureWrapper*)(void*)handle;
        if (wrapper.session.isRunning) {
            [wrapper.session stopRunning];
        }
        return MEDIA_AV_OK;
    }
}

void media_av_video_capture_destroy(MediaAVVideoCapture handle) {
    if (!handle) return;

    @autoreleasepool {
        // Transfer ownership and let ARC release
        MediaVideoCaptureWrapper* wrapper = (MediaVideoCaptureWrapper*)CFBridgingRelease((void*)handle);
        if (wrapper.session.isRunning) {
            [wrapper.session stopRunning];
        }
        // wrapper and all its properties will be released by ARC
    }
}

// Audio capture stubs
MediaAVAudioCapture media_av_audio_capture_create(
    const char* device_id,
    int32_t sample_rate,
    int32_t channels,
    MediaAVAudioCallback callback,
    void* user_data
) {
    set_error("Audio capture not yet implemented");
    return 0;
}

int32_t media_av_audio_capture_start(MediaAVAudioCapture handle) {
    return MEDIA_AV_ERROR;
}

int32_t media_av_audio_capture_stop(MediaAVAudioCapture handle) {
    return MEDIA_AV_ERROR;
}

void media_av_audio_capture_destroy(MediaAVAudioCapture handle) {
}
