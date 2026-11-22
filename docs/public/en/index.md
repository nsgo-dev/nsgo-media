---
title: Introduction
description: What NSGo Media is, what it does for an NSGo app and what it leaves to the app.
---

NSGo Media is a video player core for [NSGo](https://github.com/nsgo-dev/nsgo) apps. It opens a media source, decodes it with FFmpeg, keeps sound and picture in sync, plays the sound and draws the picture into a view. The app builds every control itself and drives the core through its Go API.

:::note[In development]
Playback, sound, seeking, subtitles and hardware decoding work on macOS, Windows, Linux and iOS for local use. There is no published release yet.
:::

## Why a core

NSGo's `core.Video` hands a source to each platform's own player. Formats differ by platform (AVFoundation opens no MKV, AVI or FLV), and the app gets no frame-exact clock, no frame stepping and no picture it can draw over freely. NSGo Media is for apps that need a real player, such as media players, editors' previews and course viewers.

## What the core does

- **Sources**: local files and HTTP(S) URLs with the headers the app gives, in any container and codec the bundled FFmpeg decodes, and a separate audio source played with a source's picture, as DASH serves them.
- **Playback**: play, pause, stop, fast and exact seeking, stepping one frame forward or backward, rates from 0.25× to 4× with the pitch kept, looping and snapshots.
- **Sound**: volume, mute and the audio track, on the system's default device, and passthrough of AC-3, E-AC-3, DTS, DTS-HD and TrueHD to a receiver.
- **Picture**: a view that draws the frame with contain, cover or fill, follows the stream's rotation and lets app views draw above it.
- **Subtitles**: text and bitmap tracks delivered as cues for the app to draw.
- **Streams**: HLS and DASH, on demand and live.
- **Decoding**: VideoToolbox, D3D11VA and VAAPI, falling back to software.
- **State**: playing, paused, buffering, ended and error states, position, buffered range and the chosen tracks, as NSGo signals.

Several players can play at once, each with its own view.

## What it leaves to the app

- Every control: play buttons, progress bars, menus, gestures and keyboard shortcuts.
- Drawing subtitles. The core hands over their text and bitmaps.
- Libraries, playlists, history and settings. The core persists nothing.

Recording, transcoding, DRM, disc images and receivers such as DLNA or AirPlay are out of scope.

## Platforms

| Platform | Targets | Hardware decoding | Audio |
| --- | --- | --- | --- |
| macOS 11 or later | arm64, x64 | VideoToolbox | CoreAudio |
| Windows 10 or later | x64 | D3D11VA | WASAPI |
| Linux with glibc 2.34 and libva 2 | x64 | VAAPI | PulseAudio, which PipeWire's server answers too |
| iOS 15 or later | devices, arm64 simulator | VideoToolbox | AudioQueue, without passthrough |

Android is set aside for now.

## Next

- [Quick start](quick-start/) adds the core to an app and opens a first file.
- [FFmpeg libraries](ffmpeg/) covers building, loading and shipping the libraries, and what an app that ships them owes.
