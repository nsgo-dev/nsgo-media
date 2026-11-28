# FFmpeg libraries

`tools/ffmpeg` builds the FFmpeg shared libraries the core loads, and the binding's struct layouts, and prepares the release `nsgo build` downloads desktop libraries from. Read this before changing the pinned version, a configure flag, the binding's majors or how the libraries are packaged.

## Building

```sh
go run ./tools/ffmpeg                                    # every target into dist/ffmpeg
go run ./tools/ffmpeg -target linux_x64                  # one target
go run ./tools/ffmpeg -layout internal/ffmpeg/layout.go  # regenerate the struct layouts
go run ./tools/ffmpeg -natives internal/ffmpeg/library   # the release's files and the manifest
```

It runs on macOS with Xcode and Go alone. Every download is pinned with its SHA-256 in `tools/ffmpeg/pinned.go` and kept in the work directory (`-work`, by default the user cache's `nsgo-media/ffmpeg`).

| Target | Compiler | Hardware decoding | Video encoders | Run-time needs |
| --- | --- | --- | --- | --- |
| `macos_arm64`, `macos_x64` | Xcode's clang | VideoToolbox | `h264_videotoolbox` | macOS 11 |
| `ios` (devices), `ios_simulator` (arm64) | Xcode's clang | VideoToolbox | `h264_videotoolbox` | iOS 15 |
| `windows_x64` | Zig, MinGW with the UCRT | D3D11VA, DXVA2 | `h264_nvenc`, `h264_qsv`, `h264_amf`, `h264_mf` | Windows 10 |
| `linux_x64` | Zig, glibc 2.34 | VAAPI | `h264_vaapi`, `libopenh264` | glibc 2.34 and `libva.so.2`, `libva-drm.so.2` |

- The tool runs itself as the compiler, archiver and `pkg-config` configure calls (`-as`), so it adapts MinGW's linker flags to Zig's and answers only for the pinned dependencies (`libva` from Debian 12's packages, `libxml2`, `ffnvcodec`, `libvpl`, `openh264`). Nothing on the build host is detected (`--disable-autodetect`).
- Only LGPL 2.1 parts are configured: every demuxer, parser and decoder, the H.264 encoders listed above, the file protocol, the audio filters rate changes use, and the spdif muxer, which wraps passthrough audio in IEC 61937 bursts. There are no other encoders (and never GPL encoders such as x264), other muxers, devices or network protocols; HTTP(S) is read in Go through a custom `AVIOContext`, so no TLS library is linked. zlib is linked statically on Windows and Linux.
- On Windows, `nv-codec-headers` (MIT) and `AMF` headers (MIT) are header-only and load the vendor drivers at run time; Intel's `libvpl` dispatcher (MIT) is linked statically into `libavcodec` and loads the system's VPL runtime; `h264_mf` uses Windows Media Foundation and encodes even without a GPU. On Linux, Cisco's `openh264` (BSD 2-Clause) is linked statically into `libavcodec` as the software fallback when VAAPI is unavailable.
- libxml2 (MIT), which the DASH demuxer parses manifests with, is built in its smallest configuration and linked statically into libavformat on every target, adding about 1 MB. Its `xmlCleanupParser` is made a no-op: the DASH demuxer calls it after each manifest, and it would free the library's state under another player parsing.
- One line of FFmpeg is patched (`ffmpegPatches` in `build.go`): an interrupted `dash_read_packet` returns `AVERROR_EXIT`, where it returned 0 with no packet and `av_read_frame` called it again without end, so a seek or `Close` during a DASH read hung.
- Each archive, `ffmpeg-<version>-<target>.zip`, holds the six libraries under their run-time names (`libavutil.61.dylib`, `avutil-61.dll`, `libavutil.so.61`) and the notices the licenses ask to ship with them: `COPYING.LGPLv2.1`, `COPYING.libxml2`, `COPYING.libvpl` (Windows), `COPYING.openh264` (Linux), `ffmpeg.patch` (the patch as a unified diff, headed by who changed the file, when and why) and `BUILD.txt` with the sources, the changes and the configure line. `dist/ffmpeg/source` gets FFmpeg's release tarball and `ffmpeg.patch`, the libraries' complete source.
- macOS libraries have `@rpath` install names and an `@loader_path` run path; Linux ones a `$ORIGIN` run path, so each finds its siblings wherever they are.
- iOS loads native code only from frameworks, so an iOS archive holds six frameworks instead, `libavutil.framework/libavutil` and so on, each with an `Info.plist` for its platform: install names `@rpath/libavutil.framework/libavutil`, the libraries they link renamed to match, and an `@loader_path/..` run path. They are unsigned, as the app's signing signs them. Their host tools, such as swscale's assembly generator, build for the Mac, so `SDKROOT` is not set for iOS.

## Adaptive streams

The HLS and DASH demuxers open playlists, segments and keys themselves, and only by URLs of a protocol FFmpeg has. So the core gives FFmpeg every HTTP(S) URL as a path under `/nsgo-media/` (`https://cdn.example/a.m3u8` becomes `/nsgo-media/https/cdn.example/a.m3u8`), which it takes for a file and resolves relative references against, and its `io_open` and `io_close2` hand each one back to Go. Go rewrites every manifest it serves so that absolute and root-relative references are such paths, downloads segments with their byte ranges, decrypts AES-128, and measures the bandwidth. Libraries without libxml2, such as Homebrew's, play HLS but not DASH.

## Publishing

The desktop libraries are published as a release of this repository, whose tag is `releaseTag` in `tools/ffmpeg/natives.go` (`ffmpeg-9.0.2-2`), and declared in `internal/ffmpeg/library/nsgo-natives.yaml` for NSGo's tool ([native libraries](https://github.com/nsgo-dev/nsgo/blob/main/docs/public/en/native-libraries.md)). After building every target, `-natives` writes from the archives in `dist/ffmpeg`:

- `dist/ffmpeg/release`, the release's assets: each desktop target's six libraries as `<target>-<file>`, as NSGo's tool downloads libraries rather than archives, every archive, iOS' included, and FFmpeg's release tarball with `ffmpeg.patch`, the libraries' complete source.
- The manifest, giving each library of `macos_arm64`, `macos_x64`, `windows_x64` and `linux_x64` its URL in the release, its SHA-256 and its notices, which it copies from the archives into `internal/ffmpeg/library/notices`: `COPYING.LGPLv2.1` and the target's `NOTICE-BUILD.txt` for every library, `COPYING.libxml2` and `NOTICE-ffmpeg.patch` for libavformat, `COPYING.libvpl` for Windows' libavcodec, and `COPYING.openh264` for Linux' libavcodec. NSGo's tool takes a file named `COPYING` for a license it must recognize and one named `NOTICE` for a notice beside it. `windows_arm64` and `linux_arm64` declare none.

```sh
gh release create ffmpeg-9.0.2-2 dist/ffmpeg/release/* --latest=false --title "FFmpeg 9.0.2 libraries (2)"
```

A release's assets never change: libraries built again, for a new version, flag or patch, go out under the next tag, and the manifest is written again and committed with it. A test checks that the manifest declares the libraries of `releaseTag`.

## Packaging an app

On desktops, `nsgo build` of an app importing the core downloads the target's libraries the manifest declares, checks their SHA-256 and keeps them in the user cache's `nsgo/natives`. It puts them into a macOS app's `Contents/Frameworks`, joining both architectures with `lipo` for `macos_universal`, or beside a Windows or Linux executable, and signs them before the app, so its disk image, installers and notarization hold them. The notices go into the app's `THIRD_PARTY_NOTICES.txt` and `core.ThirdPartyNotices()`. NSGo's license policy warns of their LGPL as copyleft; an app with `licenses.copyleft: error` adds `github.com/nsgo-dev/nsgo-media` to `licenses.allow`.

- `nsgo dev`, and `nsgo test` with a desktop `-target`, put them in too; `nsgo test` without one, as `go run` and `go test`, puts none in. `NSGO_MEDIA_FFMPEG` still comes first, so a checkout runs with libraries it built by naming their directory, such as an unpacked archive of `dist/ffmpeg`.
- A target NSGo's manifest does not cover packages nothing from it: iOS, Android and the web.
- `nsgo-module.yaml` lists the targets of the table above, which a test keeps in step, and a short form of the duties below, so `nsgo add` shows them before writing anything and `nsgo check` warns of a target it lacks, such as `windows_arm64`, `linux_arm64` or Android.

On iOS, `go tool nsgo-media bundle` puts the frameworks into the app `nsgo build` wrote, its `Frameworks`, with every notice of the archive in `FFmpeg/`. It refuses an archive without `COPYING.LGPLv2.1` or `BUILD.txt`, takes each package's target from the end of its name, and skips desktop packages.

```sh
go get -tool github.com/nsgo-dev/nsgo-media/cmd/nsgo-media
go tool nsgo build -target ios
go tool nsgo-media bundle dist/*      # -libs DIR to take the archives from elsewhere
```

- The frameworks are the archives `go run ./tools/ffmpeg` built; nothing is downloaded. `bundle` looks in `dist/ffmpeg` of the current directory, as in this repository, or else in `dist/ffmpeg` of the directory `go list -m` gives for this module, which for another app is the local checkout its `replace` directive names. Where neither has them, it stops and says to build them with `go run ./tools/ffmpeg` or pass `-libs`.
- An iOS app is signed again when it is signed: a simulator's always, ad hoc, and a device's when `sign.profile` signed it, the frameworks first with its identity (`-sign` names another), then the app keeping its identifier and entitlements. An unsigned device app keeps unsigned frameworks for the signing that installs it, and the IPA beside it is written again from it. `nsgo build -run` and `nsgo dev` install the app before `bundle` can run, so for a device build with `sign.profile`, bundle, then install with `xcrun devicectl device install app`.

### What an app that ships them owes

The libraries are LGPL 2.1, so an app, closed or not, ships them only as replaceable shared libraries, as `nsgo build` and `bundle` put them, with their notices, which `THIRD_PARTY_NOTICES.txt` holds on desktops and `FFmpeg/` on iOS, and besides:

- Offers their source: the files of `dist/ffmpeg/source`, which the release holds too (`ffmpeg-9.0.2.tar.xz` and `ffmpeg.patch`), beside the app's downloads on a server it controls, for as long as it distributes that build, or shipped with the app. The LGPL 2.1 asks for the source from the place the app is downloaded from, so a link to this repository's release does not settle it.
- Says in its about box or documentation that it uses libraries of the FFmpeg project under the LGPL 2.1, and where their source is; an about box can show `core.ThirdPartyNotices()`.
- Allows, in any license terms it has, changing the libraries and the reverse engineering needed to debug them, and claims no FFmpeg trademark.
- Settles codec patents itself; the LGPL grants none.
- On iOS, ships them as the separate dynamic frameworks `bundle` puts in, never linked into the executable. Whether an App Store app's signing and terms let users replace them is the app's own question; the LGPL does not answer it.

## Binding

`internal/ffmpeg` names the majors of FFmpeg 9 (avutil 61, avcodec 63, avformat 63, avfilter 12, swscale 10, swresample 7) and refuses others, as fields move only between majors. `layout.go` holds the offsets of the fields it reads, which `-layout` dumps with clang from the pinned headers for LP64 and Windows' LLP64 and refuses to write where they differ. Enum values it can look up by name, such as pixel formats, it looks up at run time.

The libraries load from the first of: the directory `NSGO_MEDIA_FFMPEG` names, the app bundle's `Contents/Frameworks` on macOS, the executable's directory, then the system's FFmpeg (Homebrew's prefixes on macOS, the loader's search elsewhere). On iOS they load from the frameworks in `NSGO_MEDIA_FFMPEG`, which tests on the simulator use, or in the app's `Frameworks`, and nowhere else.
