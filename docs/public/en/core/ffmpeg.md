---
title: FFmpeg libraries
description: Build FFmpeg's LGPL libraries, see where the core loads them from, put them into your app and learn what shipping them owes.
---

The core reaches FFmpeg through purego, without cgo, and loads its shared libraries at run time. It names the majors of FFmpeg 9 and refuses other versions instead of guessing their layouts.

## Building them

`tools/ffmpeg` builds the libraries for each target at one pinned version, from a checkout of the repository. It runs on macOS with Xcode and Go alone, and every download is pinned with its SHA-256.

```sh
go run ./tools/ffmpeg                    # every target into dist/ffmpeg
go run ./tools/ffmpeg -target linux_x64  # one target
```

| Target | Hardware decoding | Run-time needs |
| --- | --- | --- |
| `macos_arm64`, `macos_x64` | VideoToolbox | macOS 11 |
| `ios`, `ios_simulator` (arm64) | VideoToolbox | iOS 15 |
| `windows_x64` | D3D11VA, DXVA2 | Windows 10 |
| `linux_x64` | VAAPI | glibc 2.34, `libva.so.2` and `libva-drm.so.2` |

Only LGPL 2.1 parts are configured: every demuxer, parser and decoder, the file protocol, the audio filters rate changes use and the spdif muxer for passthrough. There are no encoders, other muxers, devices or network protocols; HTTP(S) is read in Go, so no TLS library is linked. libxml2 (MIT) is linked into libavformat for DASH.

Each archive, `ffmpeg-<version>-<target>.zip`, holds the six libraries, or six frameworks for iOS, and the notices their licenses ask to ship with them: `COPYING.LGPLv2.1`, `COPYING.libxml2`, `ffmpeg.patch` and `BUILD.txt`. `dist/ffmpeg/source` gets FFmpeg's release tarball and the patch, the libraries' complete source.

## Where they load from

The core takes the first it finds:

1. The directory the `NSGO_MEDIA_FFMPEG` environment variable names.
2. The app bundle's `Contents/Frameworks` on macOS.
3. The executable's directory.
4. The system's FFmpeg 9: Homebrew's prefixes on macOS, the loader's search elsewhere.

On iOS they load only from frameworks: in `NSGO_MEDIA_FFMPEG`, which simulator tests use, or in the app's `Frameworks`.

## Packaging an app

`go tool nsgo-media bundle` puts the libraries into the packages `nsgo build` wrote: a macOS app's `Contents/Frameworks`, an iOS app's `Frameworks`, and the directory of a Windows or Linux executable, with every notice of the archive in `FFmpeg/`.

```sh
go get -tool github.com/oneo-me/nsgo-media/cmd/nsgo-media
go tool nsgo build -installers=false
go tool nsgo-media bundle dist/*   # -libs DIR to take the archives from elsewhere
```

- It takes the archives `tools/ffmpeg` built and downloads nothing. It looks in `dist/ffmpeg` of the current directory, then in `dist/ffmpeg` of the module's directory, which for your app is the local checkout its `replace` directive names.
- It takes each package's target from the end of its name, and joins both architectures' libraries with `lipo` for `macos_universal`.
- A macOS app is signed again with the identity it has (`-sign` names another). An app `nsgo build` notarized needs notarizing again.
- An iOS app is signed again when it is signed, and the IPA beside it is written again. `nsgo build -run` and `nsgo dev` install the app before `bundle` can run, so for a device build with `sign.profile`, bundle, then install it with `xcrun devicectl device install app`.
- The installers `nsgo build` makes are made before `bundle` runs and lack the libraries, so build with `-installers=false` and make installers afterwards.

## What an app that ships them owes

The libraries are LGPL 2.1, so an app, closed or not, ships them only as replaceable shared libraries, as `bundle` puts them, and besides:

- Offers their source: the files of `dist/ffmpeg/source`, beside the app's downloads on a server it controls, for as long as it distributes that build, or shipped with the app.
- Says in its about box or documentation that it uses libraries of the FFmpeg project under the LGPL 2.1, and where their source is.
- Allows, in any license terms it has, changing the libraries and the reverse engineering needed to debug them, and claims no FFmpeg trademark.
- Settles codec patents itself; the LGPL grants none.
- On iOS, ships them as the separate dynamic frameworks `bundle` puts in, never linked into the executable. Whether an App Store app's signing and terms let users replace them is the app's own question; the LGPL does not answer it.
