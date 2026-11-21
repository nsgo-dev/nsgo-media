# Requirements

NSGo Media is a video player core for [NSGo](https://github.com/nsgo-dev/nsgo) apps. It opens a media source, decodes it with FFmpeg, keeps audio and video in sync, plays the sound and renders the picture into a view. The app builds every control itself and drives the core through its Go API.

## Problem

NSGo's `core.Video` hands a source to each platform's own player. Formats differ by platform (AVFoundation opens no MKV, AVI or FLV), and the app gets no frame-exact clock, no frame stepping and no picture it can draw over freely. Apps that need a real player, such as media players, editors' previews and course viewers, have no route on NSGo today.

## Users

- Developers of NSGo apps who embed playback and want their own controls, overlays and subtitles.
- The developer's end users only indirectly: they see the picture, hear the sound and use the app's controls.

## First version

Desktop targets: macOS (arm64 and x64), Windows x64 and Linux x64.

| Area | Included |
| --- | --- |
| Sources | Local files and HTTP(S) URLs with the headers the app gives, such as `Referer`, `User-Agent` and `Cookie`, in any container and codec the bundled FFmpeg decodes; a separate audio source played with a source's picture, as DASH serves them |
| Information | Duration, tracks (video, audio, subtitle) with their codecs and languages, picture size and rotation, container metadata, chapters |
| Playback | Play, pause, stop; seek to a time, fast to a keyframe or exact to a frame; step one frame forward while paused; rate from 0.25× to 4× with the pitch kept; loop |
| Sound | Volume, mute, choosing the audio track; output on the system's default device |
| Picture | A view that draws the current frame with contain, cover or fill, follows the stream's rotation, and lets app views draw above it |
| Subtitles | Text subtitle tracks of the source and external SRT, WebVTT and ASS files in UTF-8 or UTF-16, delivered to the app as timed text cues; the app draws them |
| State | Playing, paused, buffering, ended and error states, position, buffered range and the chosen tracks, as NSGo signals the app's views read |
| Decoding | Hardware decoding through FFmpeg where the platform offers it, falling back to software decoding on failure |
| Players | Several players at once, each with its own view |
| Distribution | FFmpeg shared libraries this repository builds locally, LGPL components only; nothing is published online |

## After the first version

| Area | Included |
| --- | --- |
| Adaptive streams | HLS and DASH over HTTP(S), on demand and live: a live stream reports no duration and, keeping only a sliding window, no seeking. The variant follows the measured bandwidth unless the app lists the variants and chooses one. HLS segments encrypted with AES-128 |
| Bitmap subtitles | PGS, DVD and DVB subtitle tracks of the source and external VobSub files (`.idx` with its `.sub`), delivered as the same cues as text, each with its seek generation and RGBA bitmaps placed on the source's canvas. A cue replaces the one before it, as PGS updates and clears its picture; cues hold while paused and none of the old position stays after a seek. The core places a canvas over the picture as the view fits and turns it, and the app draws the bitmaps |

## Out of scope

- Any control UI: play buttons, progress bars, menus, gestures and keyboard shortcuts belong to the app.
- Media libraries, playlists, history and remote sources such as SMB, WebDAV or media servers.
- Recording, capture, transcoding and encoding.
- DRM, disc images and protocol receivers such as DLNA or AirPlay.
- Rendering subtitles into the picture, and ASS styling beyond text.
- Mobile targets, the web and mini programs; the [roadmap](roadmap.md) orders them.

## Constraints

- No cgo and no hand-written C: FFmpeg and system audio are reached through purego or `syscall`, as NSGo reaches its systems.
- Only FFmpeg's LGPL components, loaded as shared libraries, so closed-source apps can ship the core. On Linux they need glibc 2.34 and libva 2, which desktop distributions install.
- The core uses only NSGo's public API, so it never needs a fork of NSGo.
- [shuax/media](https://github.com/shuax/media) and [shuax/player](https://github.com/shuax/player) are design references only. They carry no license, so no code is copied from them.
