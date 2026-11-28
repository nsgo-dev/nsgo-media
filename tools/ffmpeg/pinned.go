package main

import "runtime"

// FFmpegVersion is the FFmpeg release the libraries are built from. The
// binding in internal/ffmpeg names the library majors it carries.
const FFmpegVersion = "9.0.2"

// download is a pinned file: fetched once into the work directory and
// checked against its SHA-256 before use.
type download struct {
	name   string
	url    string
	sha256 string
}

var (
	ffmpegSource = download{
		name:   "ffmpeg-" + FFmpegVersion + ".tar.xz",
		url:    "https://ffmpeg.org/releases/ffmpeg-" + FFmpegVersion + ".tar.xz",
		sha256: "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e",
	}
	// nasm assembles FFmpeg's x86 code for macOS x64, Windows and Linux.
	nasmSource = download{
		name:   "nasm-3.01.tar.xz",
		url:    "https://www.nasm.us/pub/nasm/releasebuilds/3.01/nasm-3.01.tar.xz",
		sha256: "b7324cbe86e767b65f26f467ed8b12ad80e124e3ccb89076855c98e43a9eddd4",
	}
	// zlib is linked statically into Windows' and Linux' libraries, where
	// the system has none to link against; macOS uses the SDK's.
	zlibSource = download{
		name:   "zlib-1.3.1.tar.gz",
		url:    "https://zlib.net/fossils/zlib-1.3.1.tar.gz",
		sha256: "9a93b2b7dfdac77ceba5a558a580e74667dd6fede4585b91eefb60f03b72df23",
	}
	// libxml2 parses DASH manifests. It is linked statically into
	// every target's libavformat.
	libxml2Source = download{
		name:   "libxml2-2.15.4.tar.xz",
		url:    "https://download.gnome.org/sources/libxml2/2.15/libxml2-2.15.4.tar.xz",
		sha256: "98087fd181d9070724f3fbc65c7377db03038eb92bd882374daff44940138821",
	}
	// libva's headers and libraries are what Linux' libraries link VAAPI
	// against; at run time the system's libva.so.2 is loaded.
	libvaPackages = []download{
		{
			name:   "libva-dev_2.17.0-1_amd64.deb",
			url:    "https://deb.debian.org/debian/pool/main/libv/libva/libva-dev_2.17.0-1_amd64.deb",
			sha256: "ea3ecdc7f01eb4519701ad17616dbd3e1afb28745d61f11f5af4d26bf6f7db4d",
		},
		{
			name:   "libva2_2.17.0-1_amd64.deb",
			url:    "https://deb.debian.org/debian/pool/main/libv/libva/libva2_2.17.0-1_amd64.deb",
			sha256: "e6acf0c4071c01df7c35d18a97768047294dc43d252caae9ccddc1e507456695",
		},
		{
			name:   "libva-drm2_2.17.0-1_amd64.deb",
			url:    "https://deb.debian.org/debian/pool/main/libv/libva/libva-drm2_2.17.0-1_amd64.deb",
			sha256: "dc0a0cadbdc001df3d89855d8054aacb332a731f98c7741ce580492b0e01e605",
		},
	}
	// nv-codec-headers are FFmpeg's MIT headers for dynamically loading
	// NVIDIA's NVENC driver on Windows.
	nvCodecHeadersSource = download{
		name:   "nv-codec-headers-12.1.14.1.tar.gz",
		url:    "https://github.com/FFmpeg/nv-codec-headers/archive/refs/tags/n12.1.14.1.tar.gz",
		sha256: "f40c8a27417eb2688412da32985e382e811196311c4d103d1fcaafad2876cfc1",
	}
	// AMF headers (MIT) let FFmpeg dynamically load AMD's AMF runtime on
	// Windows.
	amfHeadersSource = download{
		name:   "AMF-headers-v1.5.3.tar.gz",
		url:    "https://github.com/GPUOpen-LibrariesAndSDKs/AMF/releases/download/v1.5.3/AMF-headers-v1.5.3.tar.gz",
		sha256: "0f521ad98041f8001147cded4a660127f18b1c27a801a5cd691bda1fa48381be",
	}
	// libvpl (MIT) is Intel's dispatcher for QSV, linked statically into
	// Windows' libavcodec; at run time it loads the system's VPL runtime.
	libvplSource = download{
		name:   "libvpl-2.17.0.tar.gz",
		url:    "https://github.com/intel/libvpl/archive/refs/tags/v2.17.0.tar.gz",
		sha256: "4de3e2faf1e8307fb282e4a43f443191810f6a6b0a484fffa7995ba1c814c6ec",
	}
	// openh264 (BSD-2-Clause) is linked statically into Linux' libavcodec
	// as the software H.264 encoder when VAAPI is unavailable.
	openh264Source = download{
		name:   "openh264-2.7.0.tar.gz",
		url:    "https://github.com/cisco/openh264/archive/refs/tags/v2.7.0.tar.gz",
		sha256: "5178775c8b440073e70920e97d195efff721c0e56d0a78cc82c919e95d54924d",
	}
	// libvpx (BSD 3-Clause) is linked statically into every target's
	// libavcodec as the VP9 encoder.
	libvpxSource = download{
		name:   "libvpx-1.17.0.tar.gz",
		url:    "https://github.com/webmproject/libvpx/archive/refs/tags/v1.17.0.tar.gz",
		sha256: "1020f184046187baa2985dbde38e0691f49c44088bca7a1842b0236c6081dc0a",
	}
	// libopus (BSD 3-Clause) is linked statically into every target's
	// libavcodec as the Opus encoder; FFmpeg's own is experimental.
	opusSource = download{
		name:   "opus-1.5.2.tar.gz",
		url:    "https://github.com/xiph/opus/releases/download/v1.5.2/opus-1.5.2.tar.gz",
		sha256: "65c1d2f78b9f2fb20082c38cbe47c951ad5839345876e46941612ee87f9a7ce1",
	}
)

const zigVersion = "0.17.1"

// zigToolchain is the Zig that cross-compiles Windows and Linux on this Mac.
func zigToolchain() download {
	if runtime.GOARCH == "amd64" {
		return download{
			name:   "zig-x86_64-macos-" + zigVersion + ".tar.xz",
			url:    "https://ziglang.org/download/" + zigVersion + "/zig-x86_64-macos-" + zigVersion + ".tar.xz",
			sha256: "9af07b20319fbd0c92ee46ad23e81d6a782b1aaea2cfb8dcbff35f1d2c7ddbb7",
		}
	}
	return download{
		name:   "zig-aarch64-macos-" + zigVersion + ".tar.xz",
		url:    "https://ziglang.org/download/" + zigVersion + "/zig-aarch64-macos-" + zigVersion + ".tar.xz",
		sha256: "75f1bc7a1e9852f17ec9a0c1895f767c4ec56d46bc8a834920eece1d0359dd8d",
	}
}
