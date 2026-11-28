---
title: FFmpeg libraries
description: Build FFmpeg's LGPL libraries, see where the core loads them from, how they get into your app and what shipping them owes.
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

The libraries and iOS frameworks are published as a release of the repository, [`ffmpeg-9.0.2-3`](https://github.com/nsgo-dev/nsgo-media/releases/tag/ffmpeg-9.0.2-3), and the core declares them for NSGo's tool. `nsgo build` downloads them, checks their SHA-256 and puts them into a macOS app's `Contents/Frameworks`, joining both architectures for `macos_universal`, beside a Windows or Linux executable, or into an iOS app's `Frameworks`. It signs them with the app, so its disk image, installers, IPA and notarization hold them, and puts their notices into the app's `THIRD_PARTY_NOTICES.txt`, which `core.ThirdPartyNotices()` returns. There is no step after it.

- `nsgo dev`, `nsgo build -run`, and `nsgo test` with a `-target`, put them in too, on iOS as on desktops. To run with libraries you built, name their directory, such as an unpacked archive of `dist/ffmpeg`, with `NSGO_MEDIA_FFMPEG`, which comes first.
- Your app's Debian and Arch packages depend on what the core loads from the system: libva and PulseAudio's client library on `linux_x64`, and on `linux_arm64`, where nothing is bundled, the system's FFmpeg 9 too. Your `nsgo.yaml` needs none of them.
- NSGo's license policy warns of their LGPL as copyleft. If your app sets `licenses.copyleft: error`, add `github.com/nsgo-dev/nsgo-media` to `licenses.allow`.

## What an app that ships them owes

The libraries are LGPL 2.1, so an app, closed or not, ships them only as replaceable shared libraries with their notices, as `nsgo build` puts them, and besides:

- Offers their source: the files of `dist/ffmpeg/source`, which the release holds too (`ffmpeg-9.0.2.tar.xz` and `ffmpeg.patch`), beside the app's downloads on a server it controls, for as long as it distributes that build, or shipped with the app. A link to this repository's release does not settle it.
- Says in its about box or documentation that it uses libraries of the FFmpeg project under the LGPL 2.1, and where their source is; an about box can show `core.ThirdPartyNotices()`.
- Allows, in any license terms it has, changing the libraries and the reverse engineering needed to debug them, and claims no FFmpeg trademark.
- Settles codec patents itself; the LGPL grants none.
- On iOS, ships them as the separate dynamic frameworks `nsgo build` puts in, never linked into the executable. Whether an App Store app's signing and terms let users replace them is the app's own question; the LGPL does not answer it.
