# Requirements

NSGo Media is a video player core for [NSGo](https://github.com/nsgo-dev/nsgo) apps. It opens a media source, decodes it with FFmpeg, keeps audio and video in sync, plays the sound and renders the picture into a view. The app builds every control itself and drives the core through its Go API.

## Problem

NSGo's `core.Video` hands a source to each platform's own player. Formats differ by platform (AVFoundation opens no MKV, AVI or FLV), and the app gets no frame-exact clock, no frame stepping and no picture it can draw over freely. Apps that need a real player, such as media players, editors' previews and course viewers, have no route on NSGo today.

## Users

- [NSGo Player](app.md), the first-party app in `app/`: whatever it needs from playback, the core provides first, and its first version on macOS sets the core's next goals.
- Developers of NSGo apps who embed playback and want their own controls, overlays and subtitles.
- Other NSGo apps (such as remote desktop clients and servers) that share NSGo Media's bundled FFmpeg for low-latency video encoding and decoding through the `codec` package.
- The developer's end users only indirectly: they see the picture, hear the sound and use the app's controls.

## Built

Targets: macOS (arm64 and x64), Windows x64 and Linux x64, and iOS as its row says.

| Area | Included |
| --- | --- |
| Sources | Local files and HTTP(S) URLs with the headers the app gives, such as `Referer`, `User-Agent` and `Cookie`, in any container and codec the bundled FFmpeg decodes; a separate audio source played with a source's picture, as DASH serves them |
| Information | Duration, tracks (video, audio, subtitle) with their codecs and languages, picture size and rotation, container metadata, chapters |
| Playback | Play, pause, stop; seek to a time, fast to a keyframe or exact to a frame; step one frame forward or backward while paused; a snapshot of the frame showing; rate from 0.25× to 4× with the pitch kept; loop |
| Sound | Volume, mute, choosing the audio track; output on the system's default device |
| Picture | A view that draws the current frame with contain, cover or fill, follows the stream's rotation, outputs native HDR (HDR10 and HLG) on Windows and Linux HDR displays when supported and tone-maps HDR10 and HLG to SDR on SDR displays, software decoding and snapshots while leaving SDR unchanged, and lets app views draw above it |
| Subtitles | Text subtitle tracks of the source and external SRT, WebVTT and ASS files in UTF-8 or UTF-16, delivered to the app as timed text cues; the app draws them |
| State | Playing, paused, buffering, ended and error states, position, buffered range and the chosen tracks, as NSGo signals the app's views read |
| Decoding | Hardware decoding through FFmpeg where the platform offers it, falling back to software decoding on failure |
| Players | Several players at once, each with its own view |
| Distribution | FFmpeg shared libraries this repository builds, LGPL components only, including LGPL-compatible hardware and software video encoders on desktops. The desktop ones are published as a release of it, with their licenses, source diff and source, and declared for NSGo's tool, so `nsgo build` puts them into a desktop app's packages and installers, signed and notarized with it; `bundle` puts the iOS frameworks into an iOS app |
| Codec | A public `codec` package for other NSGo apps: low-latency H.264 encoding from BGRA or NV12 frames (no B-frames, on-demand keyframes, dynamic bit rate) with desktop hardware encoders and software fallback, low-latency decoding into RGBA frames for NSGo views (hardware first with software fallback), and probing of the machine's available encoders and decoders |
| Adaptive streams | HLS and DASH over HTTP(S), on demand and live: a live stream reports no duration and, keeping only a sliding window, no seeking. The variant follows the measured bandwidth unless the app lists the variants and chooses one. HLS segments encrypted with AES-128 |
| Audio passthrough | AC-3, E-AC-3 and DTS' core sent undecoded, in IEC 61937 bursts, to a receiver on HDMI or S/PDIF, and DTS-HD (High Resolution and Master Audio, whole) and TrueHD (as its stream is, Atmos and all, in MAT frames) on HDMI's high bit rate carrier, 8 channels at 192 kHz, for the codecs the app turns it on for, off by default: WASAPI in exclusive mode on Windows, a hogged CoreAudio device with an encoded format on macOS, a PulseAudio or PipeWire sink with passthrough formats on Linux. Where the device takes no high bit rate, DTS-HD passes its core if DTS passes. PCM plays instead where the device takes no such stream, refuses or fails to start it, at rates other than 1×, and for enhanced DTS that does not pass, such as DTS-ES or DTS-HD without its switch, and the state tells which plays, the device stream's rate and channels and why. Volume does not change passed-through sound and muting sends pause bursts; it stays the clock through seeking and changing the track |
| iOS | iOS 15 or later on devices (arm64) and the arm64 simulator, with everything above but passthrough, which iOS offers no app: FFmpeg as frameworks in the app, VideoToolbox decoding and an AudioQueue on the system's output. A playing player holds the audio session at Playback, so the Ring/Silent switch does not silence it, and it pauses other apps' audio. A call, Siri or another interruption pauses the player, which plays again as it ends where the system allows it; headphones unplugged pause it until the app plays. With the app's `audio` background mode, a source with sound plays on in the background and with the screen locked, its picture decoded again as the app returns; without it, going to the background pauses the player. No lock-screen or Control Center controls |
| Bitmap subtitles | PGS, DVD and DVB subtitle tracks of the source and external VobSub files (`.idx` with its `.sub`), delivered as the same cues as text, each with its seek generation and RGBA bitmaps placed on the source's canvas. A cue replaces the one before it, as PGS updates and clears its picture; cues hold while paused and none of the old position stays after a seek. The core places a canvas over the picture as the view fits and turns it, and the app draws the bitmaps |

## Goals

In the [roadmap](https://github.com/oneo-me/nsgo-media/issues/2)'s order:

- What NSGo Player needs.
- Zero-copy GPU rendering and HDR output of hardware-decoded frames on macOS and iOS (Windows and Linux have both).

## Out of scope

- Any control UI: play buttons, progress bars, menus, gestures and keyboard shortcuts belong to the app.
- Media libraries, playlists, history and remote sources such as SMB, WebDAV or media servers.
- Recording, capture or transcoding in the player (encoding is exposed only as a low-level capability in `codec` for other NSGo apps).
- DRM, disc images and protocol receivers such as DLNA or AirPlay.
- Rendering subtitles into the picture, and ASS styling beyond text.
- Android, set aside for now.
- The web and mini programs.

## Constraints

- No cgo and no hand-written C: FFmpeg and system audio are reached through purego or `syscall`, as NSGo reaches its systems.
- Only FFmpeg's LGPL components, loaded as shared libraries, so closed-source apps can ship the core. On Linux they need glibc 2.34 and libva 2, which desktop distributions install.
- The core uses only NSGo's public API, so it never needs a fork of NSGo.
- [shuax/media](https://github.com/shuax/media) and [shuax/player](https://github.com/shuax/player) are design references only. They carry no license, so no code is copied from them.
