# FFmpeg libraries

`tools/ffmpeg` builds the FFmpeg shared libraries the core loads, and the binding's struct layouts. Read this before changing the pinned version, a configure flag or the binding's majors.

## Building

```sh
go run ./tools/ffmpeg                                    # every target into dist/ffmpeg
go run ./tools/ffmpeg -target linux_x64                  # one target
go run ./tools/ffmpeg -upload v0.1.0                     # build, then upload to that existing release
go run ./tools/ffmpeg -layout internal/ffmpeg/layout.go  # regenerate the struct layouts
```

It runs on macOS with Xcode and Go alone. Every download is pinned with its SHA-256 in `tools/ffmpeg/pinned.go` and kept in the work directory (`-work`, by default the user cache's `nsgo-media/ffmpeg`).

| Target | Compiler | Hardware decoding | Run-time needs |
| --- | --- | --- | --- |
| `macos_arm64`, `macos_x64` | Xcode's clang | VideoToolbox | macOS 11 |
| `windows_x64` | Zig, MinGW with the UCRT | D3D11VA, DXVA2 | Windows 10 |
| `linux_x64` | Zig, glibc 2.34 | VAAPI | glibc 2.34 and `libva.so.2`, `libva-drm.so.2` |

- The tool runs itself as the compiler, archiver and `pkg-config` configure calls (`-as`), so it adapts MinGW's linker flags to Zig's and answers only for libva, whose headers and link libraries come from Debian 12's packages. Nothing on the build host is detected (`--disable-autodetect`).
- Only LGPL 2.1 parts are configured: every demuxer, parser and decoder, the file protocol, and the audio filters rate changes use. There are no encoders, muxers, devices or network protocols; HTTP(S) is read in Go through a custom `AVIOContext`, so no TLS library is linked. zlib is linked statically on Windows and Linux.
- Each archive, `ffmpeg-<version>-<target>.zip`, holds the six libraries under their run-time names (`libavutil.61.dylib`, `avutil-61.dll`, `libavutil.so.61`), `COPYING.LGPLv2.1` and `BUILD.txt` with the source and the configure line, which the LGPL asks to ship with them. `SHA256SUMS` lists the archives.
- macOS libraries have `@rpath` install names and an `@loader_path` run path; Linux ones a `$ORIGIN` run path, so each finds its siblings wherever they are.

## Binding

`internal/ffmpeg` names the majors of FFmpeg 9 (avutil 61, avcodec 63, avformat 63, avfilter 12, swscale 10, swresample 7) and refuses others, as fields move only between majors. `layout.go` holds the offsets of the fields it reads, which `-layout` dumps with clang from the pinned headers for LP64 and Windows' LLP64 and refuses to write where they differ. Enum values it can look up by name, such as pixel formats, it looks up at run time.

The libraries load from the first of: the directory `NSGO_MEDIA_FFMPEG` names, the app bundle's `Contents/Frameworks` on macOS, the executable's directory, then the system's FFmpeg (Homebrew's prefixes on macOS, the loader's search elsewhere).
