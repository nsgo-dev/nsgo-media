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

## Packaging an app

`go tool nsgo-media bundle` puts the libraries into the packages `nsgo build` wrote: a macOS app's `Contents/Frameworks`, where the loader looks first, and the directory of a Windows or Linux executable, each with `FFmpeg/COPYING.LGPLv2.1` and `FFmpeg/BUILD.txt`. It takes each package's target from the end of its name and joins both architectures' libraries with `lipo` for `macos_universal`.

```sh
go get -tool github.com/oneo-me/nsgo-media/cmd/nsgo-media
go tool nsgo build -installers=false
go tool nsgo-media bundle dist/*      # -libs dist/ffmpeg to use archives tools/ffmpeg built
```

- The libraries come from the release `cmd/nsgo-media/libraries.go` pins with their checksums, downloaded once into the user cache. `tools/ffmpeg -upload` writes that file after uploading; until a release is uploaded, `bundle` needs `-libs`.
- A macOS app is signed again with the identity it has (`-sign` names another): the libraries first, with the hardened runtime and a timestamp unless ad hoc, then the app keeping its identifier, entitlements and options. An app `nsgo build` notarized needs notarizing again.
- The installers `nsgo build` makes (the disk image, the Windows installer, the Linux packages and AppImage) are made from the packages before `bundle` runs, so they lack the libraries; build with `-installers=false` and make installers afterwards. NSGo's tool carrying a third-party package's native libraries, as it carries its own terminal library, would remove this step.

## Binding

`internal/ffmpeg` names the majors of FFmpeg 9 (avutil 61, avcodec 63, avformat 63, avfilter 12, swscale 10, swresample 7) and refuses others, as fields move only between majors. `layout.go` holds the offsets of the fields it reads, which `-layout` dumps with clang from the pinned headers for LP64 and Windows' LLP64 and refuses to write where they differ. Enum values it can look up by name, such as pixel formats, it looks up at run time.

The libraries load from the first of: the directory `NSGO_MEDIA_FFMPEG` names, the app bundle's `Contents/Frameworks` on macOS, the executable's directory, then the system's FFmpeg (Homebrew's prefixes on macOS, the loader's search elsewhere).
