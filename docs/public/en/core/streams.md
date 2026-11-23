---
title: HLS and DASH
description: Play adaptive streams on demand and live, and let the bandwidth or the app choose the quality.
---

`Open` plays an HLS or DASH manifest as it plays a file, known by its media type or extension. Its playlists, segments and keys are read in Go, with the headers `player.Headers` gives.

```go
p.Open("https://cdn.example/live/index.m3u8")
```

DASH needs FFmpeg's libraries built with libxml2, as `tools/ffmpeg` builds them. Libraries without it, such as Homebrew's, play HLS but not DASH.

## Live streams

`Info.Live` tells a live stream, which reports no duration. `Info.Seekable` is false for one that keeps only a sliding window: `Seek` leaves it where it plays.

## Quality

`Info.Variants()` lists the stream's variants as video tracks, each with its `Bandwidth`. While the bandwidth chooses, `AutoVariant()` is true: every 2 s of playing, the core weighs the highest variant that fits 80% of the estimated bandwidth, taking a lower one at once and a higher one 10 s after the last change.

`SelectVariant(id)` plays the variant whose video track is `id` from the position; with -1 the bandwidth chooses again. Changing the variant decodes again from the keyframe before the position, so the picture holds for a moment. `VideoTrack()` is the playing variant's track.

```go
for _, v := range p.Info().Variants() {
	fmt.Println(v.ID, v.Width, v.Height, v.Bandwidth)
}
p.SelectVariant(-1) // back to the bandwidth's choice
```

## Encryption

HLS segments encrypted with AES-128 decrypt. DRM is out of scope.

## Separate audio

Where a site serves a stream's audio apart from its picture, pass it with `player.ExternalAudio`. Its sound stays the clock, and `Buffered` is the lesser of the two sources.

```go
p.Open(videoURL, player.Headers(h), player.ExternalAudio(audioURL, h))
```
