package ffmpeg

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg/library"
)

// Majors are the library major versions the binding's struct layouts are
// generated for (FFmpeg 9). Fields only move between majors, so any minor
// release of these loads.
var Majors = map[string]int{
	"avutil":     61,
	"swresample": 7,
	"swscale":    10,
	"avcodec":    63,
	"avformat":   63,
	"avfilter":   12,
}

// order is Majors' libraries in dependency order.
var order = []string{"avutil", "swresample", "swscale", "avcodec", "avformat", "avfilter"}

var (
	loadOnce sync.Once
	loadErr  error
	loadedAt string
)

// Load loads FFmpeg's libraries once, from the first directory of
// library.Dirs that holds them all at the majors the binding names, and
// binds their functions. Later calls return the first call's result.
func Load() error {
	loadOnce.Do(func() { loadErr = load() })
	return loadErr
}

// Dir is the directory the libraries were loaded from, empty for the
// system's search, once Load succeeded.
func Dir() string { return loadedAt }

func load() error {
	var tried []string
	for _, dir := range library.Dirs() {
		handles, err := openAll(dir)
		if err != nil {
			where := dir
			if where == "" {
				where = "the system's search path"
			}
			tried = append(tried, fmt.Sprintf("%s: %v", where, err))
			continue
		}
		if err := bind(handles); err != nil {
			return err
		}
		if err := checkVersions(); err != nil {
			return err
		}
		loadedAt = dir
		avLogSetLevel(logQuiet)
		return nil
	}
	return fmt.Errorf("ffmpeg: no FFmpeg %s libraries found (set %s to their directory):\n  %s",
		majorsString(), library.EnvDir, strings.Join(tried, "\n  "))
}

func openAll(dir string) (map[string]uintptr, error) {
	handles := map[string]uintptr{}
	for _, lib := range order {
		path := library.Path(runtime.GOOS, dir, lib, Majors[lib])
		h, err := dlopen(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", library.FileName(runtime.GOOS, lib, Majors[lib]), err)
		}
		handles[lib] = h
	}
	return handles, nil
}

func majorsString() string {
	var parts []string
	for _, lib := range order {
		parts = append(parts, fmt.Sprintf("%s %d", lib, Majors[lib]))
	}
	return strings.Join(parts, ", ")
}

// checkVersions refuses libraries whose run-time major is not the one
// their file name promised, as a renamed or rebuilt library might be.
func checkVersions() error {
	got := map[string]uint32{
		"avutil":     avutilVersion(),
		"swresample": swresampleVersion(),
		"swscale":    swscaleVersion(),
		"avcodec":    avcodecVersion(),
		"avformat":   avformatVersion(),
		"avfilter":   avfilterVersion(),
	}
	var errs []error
	for _, lib := range order {
		if major := int(got[lib] >> 16); major != Majors[lib] {
			errs = append(errs, fmt.Errorf("lib%s is version %d.%d.%d, the binding needs major %d",
				lib, got[lib]>>16, got[lib]>>8&0xff, got[lib]&0xff, Majors[lib]))
		}
	}
	return errors.Join(errs...)
}

// Version is the loaded FFmpeg's version string, such as "9.0.2".
func Version() string { return avVersionInfo() }

// sym is one function to bind: its library, C name and Go variable.
type sym struct {
	lib, name string
	fn        any
}

func bind(handles map[string]uintptr) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ffmpeg: binding: %v", r)
		}
	}()
	for _, s := range symbols {
		addr, err := dlsym(handles[s.lib], s.name)
		if err != nil {
			return fmt.Errorf("ffmpeg: lib%s has no %s: %w", s.lib, s.name, err)
		}
		purego.RegisterFunc(s.fn, addr)
	}
	return nil
}

// ptr is a C pointer argument or result.
type ptr = unsafe.Pointer

var (
	avutilVersion          func() uint32
	avVersionInfo          func() string
	avLogSetLevel          func(level int32)
	avStrerror             func(code int32, buf *byte, size uintptr) int32
	avMalloc               func(size uintptr) ptr
	avFree                 func(p ptr)
	avDictGet              func(d ptr, key string, prev ptr, flags int32) ptr
	avDictIterate          func(d ptr, prev ptr) ptr
	avDictSet              func(d *ptr, key, value string, flags int32) int32
	avDictFree             func(d *ptr)
	avFrameAlloc           func() *Frame
	avFrameFree            func(f **Frame)
	avFrameUnref           func(f *Frame)
	avFrameMoveRef         func(dst, src *Frame)
	avFrameClone           func(f *Frame) *Frame
	avFrameGetBuffer       func(f *Frame, align int32) int32
	avGetPixFmt            func(name string) int32
	avGetPixFmtName        func(f int32) string
	avGetSampleFmt         func(name string) int32
	avGetBytesPerSample    func(f int32) int32
	avHwdeviceFindType     func(name string) int32
	avHwdeviceCtxCreate    func(ref *ptr, typ int32, device ptr, opts ptr, flags int32) int32
	avHwframeTransferData  func(dst, src *Frame, flags int32) int32
	avBufferUnref          func(ref *ptr)
	avBufferRef            func(ref ptr) ptr
	avChannelLayoutDefault func(l ptr, channels int32)
	avChannelLayoutUninit  func(l ptr)
	avChannelLayoutCopy    func(dst, src ptr) int32
	avOptSet               func(obj ptr, name, value string, flags int32) int32
	avOptSetInt            func(obj ptr, name string, value int64, flags int32) int32

	swresampleVersion func() uint32
	swrAllocSetOpts2  func(s *ptr, outLayout ptr, outFmt, outRate int32, inLayout ptr, inFmt, inRate int32, logOffset int32, logCtx ptr) int32
	swrInit           func(s ptr) int32
	swrConvert        func(s ptr, out *ptr, outCount int32, in *ptr, inCount int32) int32
	swrFree           func(s *ptr)
	swrGetDelay       func(s ptr, base int64) int64

	swscaleVersion  func() uint32
	swsFreeContext  func(ctx ptr)
	swsAllocContext func() ptr
	swsScaleFrame   func(ctx ptr, dst, src *Frame) int32

	avcodecVersion          func() uint32
	avcodecFindDecoder      func(id int32) *Codec
	avcodecAllocContext3    func(c *Codec) *CodecContext
	avcodecFreeContext      func(c **CodecContext)
	avcodecParametersToCtx  func(c *CodecContext, par *CodecParameters) int32
	avcodecOpen2            func(c *CodecContext, codec *Codec, opts *ptr) int32
	avcodecSendPacket       func(c *CodecContext, p *Packet) int32
	avcodecReceiveFrame     func(c *CodecContext, f *Frame) int32
	avcodecFlushBuffers     func(c *CodecContext)
	avcodecGetName          func(id int32) string
	avcodecGetHWConfig      func(c *Codec, i int32) ptr
	avcodecDecodeSubtitle2  func(c *CodecContext, sub ptr, got *int32, p *Packet) int32
	avsubtitleFree          func(sub ptr)
	avPacketAlloc           func() *Packet
	avPacketFree            func(p **Packet)
	avPacketUnref           func(p *Packet)
	avPacketSideDataGet     func(sd ptr, nb int32, typ int32) ptr
	avcodecDescriptorByID   func(id int32) ptr
	avcodecParametersCopy   func(dst, src *CodecParameters) int32
	avcodecParametersAlloc  func() *CodecParameters
	avcodecParametersFree   func(p **CodecParameters)
	avformatVersion         func() uint32
	avformatAllocContext    func() *FormatContext
	avformatOpenInput       func(c **FormatContext, url string, fmt ptr, opts *ptr) int32
	avFindInputFormat       func(name string) ptr
	avformatFindStreamInfo  func(c *FormatContext, opts ptr) int32
	avformatCloseInput      func(c **FormatContext)
	avReadFrame             func(c *FormatContext, p *Packet) int32
	avformatSeekFile        func(c *FormatContext, stream int32, minTS, ts, maxTS int64, flags int32) int32
	avioAllocContext        func(buf ptr, size int32, write int32, opaque uintptr, read, writeFn, seek uintptr) ptr
	avioContextFree         func(c *ptr)
	avfilterVersion         func() uint32
	avfilterGraphAlloc      func() ptr
	avfilterGraphFree       func(g *ptr)
	avfilterGetByName       func(name string) ptr
	avfilterGraphCreateFilt func(ctx *ptr, filt ptr, name, args string, opaque ptr, graph ptr) int32
	avfilterLink            func(src ptr, srcPad uint32, dst ptr, dstPad uint32) int32
	avfilterGraphConfig     func(graph ptr, log ptr) int32
	avBuffersrcAddFrame     func(ctx ptr, f *Frame, flags int32) int32
	avBuffersinkGetFrame    func(ctx ptr, f *Frame) int32
)

var symbols = []sym{
	{"avutil", "avutil_version", &avutilVersion},
	{"avutil", "av_version_info", &avVersionInfo},
	{"avutil", "av_log_set_level", &avLogSetLevel},
	{"avutil", "av_strerror", &avStrerror},
	{"avutil", "av_malloc", &avMalloc},
	{"avutil", "av_free", &avFree},
	{"avutil", "av_dict_get", &avDictGet},
	{"avutil", "av_dict_iterate", &avDictIterate},
	{"avutil", "av_dict_set", &avDictSet},
	{"avutil", "av_dict_free", &avDictFree},
	{"avutil", "av_frame_alloc", &avFrameAlloc},
	{"avutil", "av_frame_free", &avFrameFree},
	{"avutil", "av_frame_unref", &avFrameUnref},
	{"avutil", "av_frame_move_ref", &avFrameMoveRef},
	{"avutil", "av_frame_clone", &avFrameClone},
	{"avutil", "av_frame_get_buffer", &avFrameGetBuffer},
	{"avutil", "av_get_pix_fmt", &avGetPixFmt},
	{"avutil", "av_get_pix_fmt_name", &avGetPixFmtName},
	{"avutil", "av_get_sample_fmt", &avGetSampleFmt},
	{"avutil", "av_get_bytes_per_sample", &avGetBytesPerSample},
	{"avutil", "av_hwdevice_find_type_by_name", &avHwdeviceFindType},
	{"avutil", "av_hwdevice_ctx_create", &avHwdeviceCtxCreate},
	{"avutil", "av_hwframe_transfer_data", &avHwframeTransferData},
	{"avutil", "av_buffer_unref", &avBufferUnref},
	{"avutil", "av_buffer_ref", &avBufferRef},
	{"avutil", "av_channel_layout_default", &avChannelLayoutDefault},
	{"avutil", "av_channel_layout_uninit", &avChannelLayoutUninit},
	{"avutil", "av_channel_layout_copy", &avChannelLayoutCopy},
	{"avutil", "av_opt_set", &avOptSet},
	{"avutil", "av_opt_set_int", &avOptSetInt},

	{"swresample", "swresample_version", &swresampleVersion},
	{"swresample", "swr_alloc_set_opts2", &swrAllocSetOpts2},
	{"swresample", "swr_init", &swrInit},
	{"swresample", "swr_convert", &swrConvert},
	{"swresample", "swr_free", &swrFree},
	{"swresample", "swr_get_delay", &swrGetDelay},

	{"swscale", "swscale_version", &swscaleVersion},
	{"swscale", "sws_freeContext", &swsFreeContext},
	{"swscale", "sws_alloc_context", &swsAllocContext},
	{"swscale", "sws_scale_frame", &swsScaleFrame},

	{"avcodec", "avcodec_version", &avcodecVersion},
	{"avcodec", "avcodec_find_decoder", &avcodecFindDecoder},
	{"avcodec", "avcodec_alloc_context3", &avcodecAllocContext3},
	{"avcodec", "avcodec_free_context", &avcodecFreeContext},
	{"avcodec", "avcodec_parameters_to_context", &avcodecParametersToCtx},
	{"avcodec", "avcodec_open2", &avcodecOpen2},
	{"avcodec", "avcodec_send_packet", &avcodecSendPacket},
	{"avcodec", "avcodec_receive_frame", &avcodecReceiveFrame},
	{"avcodec", "avcodec_flush_buffers", &avcodecFlushBuffers},
	{"avcodec", "avcodec_get_name", &avcodecGetName},
	{"avcodec", "avcodec_get_hw_config", &avcodecGetHWConfig},
	{"avcodec", "avcodec_decode_subtitle2", &avcodecDecodeSubtitle2},
	{"avcodec", "avsubtitle_free", &avsubtitleFree},
	{"avcodec", "av_packet_alloc", &avPacketAlloc},
	{"avcodec", "av_packet_free", &avPacketFree},
	{"avcodec", "av_packet_unref", &avPacketUnref},
	{"avcodec", "av_packet_side_data_get", &avPacketSideDataGet},
	{"avcodec", "avcodec_descriptor_get", &avcodecDescriptorByID},
	{"avcodec", "avcodec_parameters_copy", &avcodecParametersCopy},
	{"avcodec", "avcodec_parameters_alloc", &avcodecParametersAlloc},
	{"avcodec", "avcodec_parameters_free", &avcodecParametersFree},

	{"avformat", "avformat_version", &avformatVersion},
	{"avformat", "avformat_alloc_context", &avformatAllocContext},
	{"avformat", "avformat_open_input", &avformatOpenInput},
	{"avformat", "av_find_input_format", &avFindInputFormat},
	{"avformat", "avformat_find_stream_info", &avformatFindStreamInfo},
	{"avformat", "avformat_close_input", &avformatCloseInput},
	{"avformat", "av_read_frame", &avReadFrame},
	{"avformat", "avformat_seek_file", &avformatSeekFile},
	{"avformat", "avio_alloc_context", &avioAllocContext},
	{"avformat", "avio_context_free", &avioContextFree},

	{"avfilter", "avfilter_version", &avfilterVersion},
	{"avfilter", "avfilter_graph_alloc", &avfilterGraphAlloc},
	{"avfilter", "avfilter_graph_free", &avfilterGraphFree},
	{"avfilter", "avfilter_get_by_name", &avfilterGetByName},
	{"avfilter", "avfilter_graph_create_filter", &avfilterGraphCreateFilt},
	{"avfilter", "avfilter_link", &avfilterLink},
	{"avfilter", "avfilter_graph_config", &avfilterGraphConfig},
	{"avfilter", "av_buffersrc_add_frame_flags", &avBuffersrcAddFrame},
	{"avfilter", "av_buffersink_get_frame", &avBuffersinkGetFrame},
}
