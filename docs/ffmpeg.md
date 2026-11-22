# FFmpeg libraries

`tools/ffmpeg` builds the FFmpeg shared libraries the core loads, and the binding's struct layouts. Read this before changing the pinned version, a configure flag or the binding's majors.

## Building

```sh
go run ./tools/ffmpeg                                    # every target into dist/ffmpeg
go run ./tools/ffmpeg -target linux_x64                  # one target
go run ./tools/ffmpeg -layout internal/ffmpeg/layout.go  # regenerate the struct layouts
```

It runs on macOS with Xcode and Go alone. Every download is pinned with its SHA-256 in `tools/ffmpeg/pinned.go` and kept in the work directory (`-work`, by default the user cache's `nsgo-media/ffmpeg`).

| Target | Compiler | Hardware decoding | Run-time needs |
| --- | --- | --- | --- |
| `macos_arm64`, `macos_x64` | Xcode's clang | VideoToolbox | macOS 11 |
| `windows_x64` | Zig, MinGW with the UCRT | D3D11VA, DXVA2 | Windows 10 |
| `linux_x64` | Zig, glibc 2.34 | VAAPI | glibc 2.34 and `libva.so.2`, `libva-drm.so.2` |

- The tool runs itself as the compiler, archiver and `pkg-config` configure calls (`-as`), so it adapts MinGW's linker flags to Zig's and answers only for libva, whose headers and link libraries come from Debian 12's packages. Nothing on the build host is detected (`--disable-autodetect`).
- Only LGPL 2.1 parts are configured: every demuxer, parser and decoder, the file protocol, the audio filters rate changes use, and the spdif muxer, which wraps passthrough audio in IEC 61937 bursts. There are no encoders, other muxers, devices or network protocols; HTTP(S) is read in Go through a custom `AVIOContext`, so no TLS library is linked. zlib is linked statically on Windows and Linux.
- libxml2 (MIT), which the DASH demuxer parses manifests with, is built in its smallest configuration and linked statically into libavformat on every target, adding about 1 MB. Its `xmlCleanupParser` is made a no-op: the DASH demuxer calls it after each manifest, and it would free the library's state under another player parsing.
- One line of FFmpeg is patched (`ffmpegPatches` in `build.go`): an interrupted `dash_read_packet` returns `AVERROR_EXIT`, where it returned 0 with no packet and `av_read_frame` called it again without end, so a seek or `Close` during a DASH read hung.
- Each archive, `ffmpeg-<version>-<target>.zip`, holds the six libraries under their run-time names (`libavutil.61.dylib`, `avutil-61.dll`, `libavutil.so.61`) and the notices the licenses ask to ship with them: `COPYING.LGPLv2.1`, `COPYING.libxml2`, `ffmpeg.patch` (the patch as a unified diff, headed by who changed the file, when and why) and `BUILD.txt` with the sources, the changes and the configure line. `dist/ffmpeg/source` gets FFmpeg's release tarball and `ffmpeg.patch`, the libraries' complete source.
- macOS libraries have `@rpath` install names and an `@loader_path` run path; Linux ones a `$ORIGIN` run path, so each finds its siblings wherever they are.

## Adaptive streams

The HLS and DASH demuxers open playlists, segments and keys themselves, and only by URLs of a protocol FFmpeg has. So the core gives FFmpeg every HTTP(S) URL as a path under `/nsgo-media/` (`https://cdn.example/a.m3u8` becomes `/nsgo-media/https/cdn.example/a.m3u8`), which it takes for a file and resolves relative references against, and its `io_open` and `io_close2` hand each one back to Go. Go rewrites every manifest it serves so that absolute and root-relative references are such paths, downloads segments with their byte ranges, decrypts AES-128, and measures the bandwidth. Libraries without libxml2, such as Homebrew's, play HLS but not DASH.

## Packaging an app

`go tool nsgo-media bundle` puts the libraries into the packages `nsgo build` wrote: a macOS app's `Contents/Frameworks`, where the loader looks first, and the directory of a Windows or Linux executable, with every notice of the archive in `FFmpeg/` (a macOS app's `Contents/Resources/FFmpeg`). It refuses an archive without `COPYING.LGPLv2.1` or `BUILD.txt`. It takes each package's target from the end of its name and joins both architectures' libraries with `lipo` for `macos_universal`.

```sh
go get -tool github.com/oneo-me/nsgo-media/cmd/nsgo-media
go tool nsgo build -installers=false
go tool nsgo-media bundle dist/*      # -libs DIR to take the archives from elsewhere
```

- The libraries are the archives `go run ./tools/ffmpeg` built; nothing is downloaded. `bundle` looks in `dist/ffmpeg` of the current directory, as in this repository, or else in `dist/ffmpeg` of the directory `go list -m` gives for this module, which for another app is the local checkout its `replace` directive names. Where neither has them, it stops and says to build them with `go run ./tools/ffmpeg` or pass `-libs`.
- A macOS app is signed again with the identity it has (`-sign` names another): the libraries first, with the hardened runtime and a timestamp unless ad hoc, then the app keeping its identifier, entitlements and options. An app `nsgo build` notarized needs notarizing again.
- The installers `nsgo build` makes (the disk image, the Windows installer, the Linux packages and AppImage) are made from the packages before `bundle` runs, so they lack the libraries; build with `-installers=false` and make installers afterwards. NSGo's tool carrying a third-party package's native libraries, as it carries its own terminal library, would remove this step.

### What an app that ships them owes

The libraries are LGPL 2.1, so an app, closed or not, ships them only as replaceable shared libraries, as `bundle` puts them, and besides:

- Offers their source: the files of `dist/ffmpeg/source`, beside the app's downloads on a server it controls, for as long as it distributes that build, or shipped with the app.
- Says in its about box or documentation that it uses libraries of the FFmpeg project under the LGPL 2.1, and where their source is.
- Allows, in any license terms it has, changing the libraries and the reverse engineering needed to debug them, and claims no FFmpeg trademark.
- Settles codec patents itself; the LGPL grants none.

## Binding

`internal/ffmpeg` names the majors of FFmpeg 9 (avutil 61, avcodec 63, avformat 63, avfilter 12, swscale 10, swresample 7) and refuses others, as fields move only between majors. `layout.go` holds the offsets of the fields it reads, which `-layout` dumps with clang from the pinned headers for LP64 and Windows' LLP64 and refuses to write where they differ. Enum values it can look up by name, such as pixel formats, it looks up at run time.

The libraries load from the first of: the directory `NSGO_MEDIA_FFMPEG` names, the app bundle's `Contents/Frameworks` on macOS, the executable's directory, then the system's FFmpeg (Homebrew's prefixes on macOS, the loader's search elsewhere).
