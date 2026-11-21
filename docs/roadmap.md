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

## Later

| Goal | Depends on |
| --- | --- |
| Zero-copy GPU rendering of hardware-decoded frames | NSGo's planned `gpu` package, or a native component where overlays are not needed |
| HDR output and tone mapping | GPU rendering |
| iOS and Android | FFmpeg builds for them and a loader for their app packages |
| Audio passthrough | Requests from apps |
