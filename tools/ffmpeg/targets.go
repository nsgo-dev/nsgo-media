package main

// target is one desktop the libraries are built for.
type target struct {
	name string
	os   string // darwin, windows or linux
	arch string // FFmpeg's: arm64 or x86_64
	// zig is Zig's target triple; empty builds with Xcode's clang.
	zig string
	// flags are what this target adds to commonFlags.
	flags []string
}

var targets = []target{
	{name: "macos_arm64", os: "darwin", arch: "arm64", flags: macFlags},
	{name: "macos_x64", os: "darwin", arch: "x86_64", flags: macFlags},
	{name: "windows_x64", os: "windows", arch: "x86_64", zig: "x86_64-windows-gnu", flags: []string{
		"--target-os=mingw32", "--enable-w32threads", "--enable-d3d11va", "--enable-dxva2", "--enable-zlib",
	}},
	// glibc 2.34 is Ubuntu 22.04's, Debian 12's and RHEL 9's floor.
	{name: "linux_x64", os: "linux", arch: "x86_64", zig: "x86_64-linux-gnu.2.34", flags: []string{
		"--target-os=linux", "--enable-pthreads", "--enable-vaapi", "--enable-zlib",
	}},
}

var allTargets = func() []string {
	var names []string
	for _, t := range targets {
		names = append(names, t.name)
	}
	return names
}()

func targetByName(name string) (target, bool) {
	for _, t := range targets {
		if t.name == name {
			return t, true
		}
	}
	return target{}, false
}

// macOSMin is the oldest macOS the libraries run on.
const macOSMin = "11.0"

var macFlags = []string{
	"--target-os=darwin", "--enable-pthreads", "--enable-videotoolbox", "--enable-zlib",
	"--install-name-dir=@rpath",
}

// commonFlags keep FFmpeg to its LGPL 2.1 parts and to what playback uses:
// every demuxer, parser and decoder, the file protocol (HTTP is read in Go
// through a custom AVIOContext, so no TLS library is linked), libxml2 for
// the DASH demuxer, the filters audio rate changes use and the spdif
// muxer, which wraps passthrough audio in IEC 61937 bursts. Nothing is
// detected from the build host.
var commonFlags = []string{
	"--enable-shared", "--disable-static",
	"--disable-gpl", "--disable-nonfree", "--disable-version3",
	"--disable-autodetect",
	"--disable-programs", "--disable-doc", "--disable-debug",
	"--disable-avdevice", "--disable-network", "--disable-iconv", "--enable-libxml2",
	"--disable-encoders", "--disable-muxers", "--enable-muxer=spdif", "--disable-devices",
	"--disable-protocols", "--enable-protocol=file",
	"--disable-filters", "--enable-filter=abuffer,abuffersink,atempo,aformat,aresample,anull",
}

// libraries are FFmpeg's libraries the binding loads, in dependency order.
var libraries = []string{"avutil", "swresample", "swscale", "avcodec", "avformat", "avfilter"}
