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
