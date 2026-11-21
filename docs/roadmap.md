# Roadmap

Milestones run in order. The [requirements](requirements.md#first-version) define the first version; nothing is built yet.

## 0. Skeleton

Done: the Go module, its package layout, an empty demo app, and the check and test commands run locally. The project has no CI yet.

## 1. FFmpeg libraries

A command that builds FFmpeg's LGPL shared libraries for each desktop target at one pinned version, uploaded to a release by the same command, and the loader that finds them.

## 2. Playback without sound

Opening a source, reading its information, decoding video on a worker and drawing frames into the view on the display clock, with play, pause, seek and stop.

## 3. Sound and sync

Audio output on CoreAudio, WASAPI and PulseAudio, the audio clock driving the picture, volume, mute, rate with the pitch kept, and track choice.

## 4. Completing the first version

Hardware decoding with its fallback, exact seeking and frame stepping, HTTP buffering and its range, subtitle cues, chapters, looping and several players at once.

## 5. First release

Measured playback of 1080p and 4K at 60 fps on each desktop, the demo app as the usage example, and a tagged release with its libraries.

## Later

| Goal | Depends on |
| --- | --- |
| Zero-copy GPU rendering of hardware-decoded frames | NSGo's planned `gpu` package, or a native component where overlays are not needed |
| HDR output and tone mapping | GPU rendering |
| iOS and Android | FFmpeg builds for them and a loader for their app packages |
| Stepping backward, snapshots of the current frame | The first version's frame pipeline |
| HLS and DASH adaptive streams, bitmap subtitles, audio passthrough | Requests from apps |
