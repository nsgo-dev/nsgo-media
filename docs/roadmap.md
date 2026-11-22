# Roadmap

Milestones run in order. The [requirements](requirements.md#first-version) define the first version; all of them are built, for local use: nothing is published online. Milestones after the first version take the [later goals](#later) one at a time.

## 0. Skeleton

The Go module, its package layout, a demo app, and the check and test commands run locally. The project has no CI yet.

## 1. FFmpeg libraries

A command that builds FFmpeg's LGPL shared libraries for each desktop target at one pinned version, and the loader that finds them.

## 2. Playback without sound

Opening a source, reading its information, decoding video on a worker and drawing frames into the view on the display clock, with play, pause, seek and stop.

## 3. Sound and sync

Audio output on CoreAudio, WASAPI and PulseAudio, the audio clock driving the picture, volume, mute, rate with the pitch kept, and track choice.

## 4. Completing the first version

Hardware decoding with its fallback, exact seeking and frame stepping, HTTP buffering and its range, subtitle cues, chapters, looping and several players at once.

## 5. Local use

Measured playback of 1080p and 4K at 60 fps on each desktop ([performance](performance.md)), the demo app as the usage example, and a step putting locally built libraries into an app's packages ([packaging](ffmpeg.md#packaging-an-app)).

## 6. Stepping backward and snapshots

Stepping one frame backward while paused, from a bounded history of recent decoded frames or by decoding again from the keyframe before, and snapshots of the frame showing at the source's size.

## 7. Adaptive streams

HLS and DASH, on demand and live, with the variant chosen by the bandwidth or by the app, and HLS' AES-128 encryption. Their manifests, segments and keys are read in Go; FFmpeg gains libxml2 for DASH.

## 8. Bitmap subtitles

PGS, DVD and DVB tracks and external VobSub files decoded to RGBA bitmaps on their canvas, in the cues text subtitles use, with the placement of a canvas over the view's picture.

## 9. Audio passthrough

AC-3, E-AC-3 and DTS' core passed through in IEC 61937 bursts, which FFmpeg's spdif muxer wraps, on the three desktops, with PCM where the device, the rate or the codec does not allow it.

## 10. iOS

FFmpeg built as frameworks for iOS devices and the simulator with VideoToolbox, the loader finding them in the app, an AudioQueue output that follows interruptions, unplugged headphones and the background, `bundle` for iOS apps, and the demo's controls for touch. Verified on the simulator and once on an iPhone (2026-11-22).

## 11. High bit rate passthrough

DTS-HD and TrueHD passed through on HDMI's 8-channel, 192 kHz carrier, in DTS type IV bursts and MAT frames the spdif muxer makes, on the three desktops, with DTS-HD's core or PCM where the device takes no high bit rate. No receiver has decoded them yet.

## Later

| Goal | Depends on |
| --- | --- |
| Zero-copy GPU rendering of hardware-decoded frames | NSGo's planned `gpu` package, or a native component where overlays are not needed |
| HDR output and tone mapping | GPU rendering |
| The iOS audio session at Playback, so the Ring/Silent switch does not silence a movie, and playing in the background | NSGo letting a library hold the session ([nsgo#1931](https://github.com/nsgo-dev/nsgo/issues/1931)), or its planned `audio.Output` ([nsgo#1057](https://github.com/nsgo-dev/nsgo/issues/1057)); the background also needs the app's `audio` background mode |

Android is set aside for now (decided by the user, 2026-11-22).
