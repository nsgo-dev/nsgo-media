package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// layoutFields are the struct fields the binding reads or writes, by C
// struct. A struct listed with no fields has only its size generated.
var layoutFields = map[string][]string{
	"AVFormatContext":   {"iformat", "oformat", "pb", "ctx_flags", "nb_streams", "streams", "nb_chapters", "chapters", "url", "start_time", "duration", "bit_rate", "flags", "metadata", "interrupt_callback", "opaque", "io_open", "io_close2"},
	"AVInputFormat":     {"name", "long_name", "flags"},
	"AVOutputFormat":    {"flags"},
	"AVIOContext":       {"buffer", "pos"},
	"AVStream":          {"index", "id", "codecpar", "time_base", "start_time", "duration", "nb_frames", "disposition", "discard", "sample_aspect_ratio", "metadata", "avg_frame_rate", "r_frame_rate"},
	"AVCodecParameters": {"codec_type", "codec_id", "codec_tag", "format", "bit_rate", "profile", "width", "height", "sample_aspect_ratio", "framerate", "ch_layout", "sample_rate", "coded_side_data", "nb_coded_side_data"},
	"AVPacketSideData":  {"data", "size", "type"},
	"AVChannelLayout":   {"order", "nb_channels", "u"},
	"AVChapter":         {"id", "time_base", "start", "end", "metadata"},
	"AVPacket":          {"pts", "dts", "data", "size", "stream_index", "flags", "duration", "pos"},
	"AVFrame":           {"data", "linesize", "extended_data", "width", "height", "nb_samples", "format", "pict_type", "sample_aspect_ratio", "pts", "pkt_dts", "sample_rate", "hw_frames_ctx", "ch_layout", "best_effort_timestamp", "duration", "flags", "color_range", "colorspace", "color_trc", "color_primaries"},
	"AVCodecContext":    {"codec", "codec_type", "codec_id", "bit_rate", "time_base", "width", "height", "gop_size", "pix_fmt", "max_b_frames", "sample_fmt", "sample_rate", "ch_layout", "rc_max_rate", "rc_buffer_size", "color_primaries", "color_trc", "colorspace", "color_range", "thread_count", "thread_type", "hw_device_ctx", "hw_frames_ctx", "pkt_timebase", "extra_hw_frames", "flags", "flags2", "framerate", "frame_size", "global_quality"},
	"AVCodec":           {"name", "long_name", "type", "id", "capabilities"},
	"AVCodecHWConfig":   {"pix_fmt", "methods", "device_type"},
	"AVSubtitle":        {"format", "start_display_time", "end_display_time", "num_rects", "rects", "pts"},
	"AVSubtitleRect":    {"x", "y", "w", "h", "nb_colors", "data", "linesize", "type", "text", "ass"},
	"AVDictionaryEntry": {"key", "value"},
	"AVIOInterruptCB":   {"callback", "opaque"},
	"AVBufferRef":       {"data"},
	"AVHWFramesContext": {"device_ctx", "format", "sw_format", "width", "height", "initial_pool_size"},
	"AVHWDeviceContext": {"type", "hwctx"},
	// Stub headers stand in for d3d11.h and va/va.h, whose types these
	// hold only pointers to or integers of.
	"AVD3D11VADeviceContext": {"device", "device_context", "lock", "unlock", "lock_ctx"},
	"AVVAAPIDeviceContext":   {"display"},
}

// layoutSizes are the structs whose size the binding needs: those it
// allocates or steps through arrays of.
var layoutSizes = []string{"AVPacketSideData", "AVChannelLayout", "AVSubtitle", "AVIOInterruptCB"}

// layoutHeaders are the public headers the dump reads.
var layoutHeaders = []string{
	"libavformat/avformat.h", "libavformat/avio.h", "libavcodec/avcodec.h", "libavutil/frame.h",
	"libavutil/channel_layout.h", "libavutil/dict.h", "libavutil/hwcontext.h", "libavutil/buffer.h",
	"libavutil/hwcontext_d3d11va.h", "libavutil/hwcontext_vaapi.h",
}

// layoutStubs stand in for the system headers hwcontext_d3d11va.h and
// hwcontext_vaapi.h include: the binding reads only pointers and
// integers of their types, the same size on every desktop.
var layoutStubs = map[string]string{
	"d3d11.h": `typedef unsigned int UINT;
typedef struct ID3D11Device ID3D11Device;
typedef struct ID3D11DeviceContext ID3D11DeviceContext;
typedef struct ID3D11VideoDevice ID3D11VideoDevice;
typedef struct ID3D11VideoContext ID3D11VideoContext;
typedef struct ID3D11Texture2D ID3D11Texture2D;
`,
	"va/va.h": `typedef void *VADisplay;
typedef unsigned int VASurfaceID;
typedef unsigned int VAConfigID;
typedef struct VASurfaceAttrib VASurfaceAttrib;
`,
}

// writeLayout dumps the layouts of layoutFields from FFmpeg's headers for
// the LP64 desktops and Windows' LLP64, and writes them as Go constants.
func writeLayout(b *builder, out string) error {
	inc, err := os.MkdirTemp("", "nsgo-media-layout")
	if err != nil {
		return err
	}
	defer os.RemoveAll(inc)
	// avconfig.h is what configure generates for the public headers.
	if err := os.MkdirAll(filepath.Join(inc, "libavutil"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(inc, "libavutil", "avconfig.h"), []byte("#define AV_HAVE_BIGENDIAN 0\n#define AV_HAVE_FAST_UNALIGNED 1\n"), 0o644); err != nil {
		return err
	}
	for name, stub := range layoutStubs {
		path := filepath.Join(inc, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(stub), 0o644); err != nil {
			return err
		}
	}
	var src bytes.Buffer
	for _, h := range layoutHeaders {
		fmt.Fprintf(&src, "#include <%s>\n", h)
	}
	names := make([]string, 0, len(layoutFields))
	for name := range layoutFields {
		names = append(names, name)
	}
	slices.Sort(names)
	for i, name := range names {
		// Using each struct makes clang dump it.
		fmt.Fprintf(&src, "struct %s *nsgo_media_layout_%d;\nint nsgo_media_size_%d = sizeof(struct %s);\n", name, i, i, name)
	}
	clang, err := xcrunFind("clang")
	if err != nil {
		return err
	}
	sdk, err := exec.Command("xcrun", "--show-sdk-path").Output()
	if err != nil {
		return err
	}
	lp64, err := dumpLayouts(exec.Command(clang, "-target", "arm64-apple-macos11", "-isysroot", strings.TrimSpace(string(sdk))), b.ffmpeg, inc, src.Bytes())
	if err != nil {
		return fmt.Errorf("LP64: %w", err)
	}
	if err := b.ensureZig(); err != nil {
		return err
	}
	win := exec.Command(b.zig, "cc", "-target", "x86_64-windows-gnu")
	win.Env = append(os.Environ(), "ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"))
	llp64, err := dumpLayouts(win, b.ffmpeg, inc, src.Bytes())
	if err != nil {
		return fmt.Errorf("LLP64: %w", err)
	}

	var g bytes.Buffer
	fmt.Fprintf(&g, "// Code generated by go run ./tools/ffmpeg -layout; DO NOT EDIT.\n\n")
	fmt.Fprintf(&g, "// Offsets and sizes of FFmpeg %s's structs, the same on every 64-bit desktop.\n\npackage ffmpeg\n\nconst (\n", FFmpegVersion)
	for _, name := range names {
		a, w := lp64[name], llp64[name]
		if a == nil {
			return fmt.Errorf("no layout of %s", name)
		}
		if slices.Contains(layoutSizes, name) {
			if a.size != w.size {
				return fmt.Errorf("%s is %d bytes on LP64 and %d on Windows; generate per OS", name, a.size, w.size)
			}
			fmt.Fprintf(&g, "\tsize%s = %d\n", name, a.size)
		}
		for _, f := range layoutFields[name] {
			off, ok := a.fields[f]
			if !ok {
				return fmt.Errorf("%s has no field %s", name, f)
			}
			if w.fields[f] != off {
				return fmt.Errorf("%s.%s is at %d on LP64 and %d on Windows; generate per OS", name, f, off, w.fields[f])
			}
			fmt.Fprintf(&g, "\toff%s%s = %d\n", name, goName(f), off)
		}
	}
	fmt.Fprintf(&g, ")\n")
	code, err := format.Source(g.Bytes())
	if err != nil {
		return err
	}
	return os.WriteFile(out, code, 0o644)
}

type recordLayout struct {
	size   int
	fields map[string]int
}

var (
	layoutStart = regexp.MustCompile(`^\s*0 \| (?:struct|union) (\w+)$`)
	layoutField = regexp.MustCompile(`^\s*(\d+)(?::\d+-\d+)? \|(\s+)(.*\S)\s*$`)
	layoutSize  = regexp.MustCompile(`\[sizeof=(\d+),`)
)

// dumpLayouts runs a compiler over src with clang's record layout dump
// and parses the top-level fields of every struct it prints.
func dumpLayouts(cmd *exec.Cmd, ffmpeg, inc string, src []byte) (map[string]*recordLayout, error) {
	file := filepath.Join(inc, "layout.c")
	if err := os.WriteFile(file, src, 0o644); err != nil {
		return nil, err
	}
	cmd.Args = append(cmd.Args, "-c", "-o", filepath.Join(inc, "layout.o"), "-I", inc, "-I", ffmpeg, "-Xclang", "-fdump-record-layouts-complete", file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}
	layouts := map[string]*recordLayout{}
	var cur *recordLayout
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := layoutStart.FindStringSubmatch(line); m != nil {
			if _, seen := layouts[m[1]]; seen {
				cur = nil
				continue
			}
			cur = &recordLayout{fields: map[string]int{}}
			layouts[m[1]] = cur
			continue
		}
		if cur == nil {
			continue
		}
		if m := layoutSize.FindStringSubmatch(line); m != nil {
			cur.size, _ = strconv.Atoi(m[1])
			cur = nil
			continue
		}
		if m := layoutField.FindStringSubmatch(line); m != nil && len(m[2]) == 3 {
			off, _ := strconv.Atoi(m[1])
			decl := m[3]
			name := decl[strings.LastIndexAny(decl, " *")+1:]
			if _, ok := cur.fields[name]; !ok {
				cur.fields[name] = off
			}
		}
	}
	return layouts, sc.Err()
}

// goName turns a C field name into the Go constant's suffix: nb_streams
// is NbStreams.
func goName(f string) string {
	var sb strings.Builder
	for _, part := range strings.Split(f, "_") {
		if part != "" {
			sb.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return sb.String()
}
