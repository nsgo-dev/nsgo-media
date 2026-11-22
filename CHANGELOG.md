# Changelog

## [Unreleased]

### Added

- `player.Player` plays local files and HTTP(S) URLs with sound, on macOS, Windows and Linux, and `player.View` draws its picture with contain, cover or fill, following the stream's rotation.
- `Open` takes `player.Headers` for the HTTP requests of a source and `player.ExternalAudio` for a separate audio source, as DASH sites serve them, kept in sync with the picture.
- Play, pause, stop, fast and exact seeking, stepping one frame forward or backward, rates from 0.25× to 4× keeping the pitch, looping, volume, mute and choosing the audio track.
- The source's information, tracks and chapters, and the state, position and buffered range as NSGo signals.
- `Player.Snapshot` gives the frame showing at the source's size and rotation, or shrunk to a size, from software and hardware decoding.
- Text subtitle tracks and external SRT, WebVTT and ASS files as timed cues.
- PGS, DVD and DVB subtitle tracks and external VobSub files as cues with RGBA bitmaps on the source's canvas and their seek generation; `Player.PictureBounds` and `Cue.Place` put the bitmaps on the picture a view draws.
- HLS and DASH streams, on demand and live, read in Go: `Info.Live` and `Info.Seekable` tell a live stream, `Info.Variants` lists the variants and `Player.SelectVariant` chooses one, or lets the bandwidth choose; HLS segments encrypted with AES-128 decrypt.
- `Player.SetPassthrough` passes AC-3, E-AC-3 and DTS' core undecoded in IEC 61937 bursts to a receiver, through WASAPI's exclusive mode, a hogged CoreAudio device or a PulseAudio sink's passthrough formats, and `Player.AudioOutput` tells what plays and why PCM plays instead: the device, a rate other than 1× or enhanced DTS.
- Hardware decoding through VideoToolbox, D3D11VA and VAAPI, falling back to software.
- `tools/ffmpeg` builds FFmpeg 9's LGPL shared libraries for each desktop into `dist/ffmpeg`, with libxml2 for DASH and the spdif muxer for passthrough.
- `go tool nsgo-media bundle` puts those libraries into the packages `nsgo build` wrote, signing a macOS app again.
- A demo app with its own controls, among them stepping both ways, saving a snapshot, choosing an HLS or DASH stream's quality, bitmap subtitles drawn over the picture and a passthrough switch with the output it gives.
