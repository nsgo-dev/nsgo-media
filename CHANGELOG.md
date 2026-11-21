# Changelog

## [Unreleased]

### Added

- `player.Player` plays local files and HTTP(S) URLs with sound, on macOS, Windows and Linux, and `player.View` draws its picture with contain, cover or fill, following the stream's rotation.
- Play, pause, stop, fast and exact seeking, stepping one frame forward or backward, rates from 0.25× to 4× keeping the pitch, looping, volume, mute and choosing the audio track.
- The source's information, tracks and chapters, and the state, position and buffered range as NSGo signals.
- `Player.Snapshot` gives the frame showing at the source's size and rotation, or shrunk to a size, from software and hardware decoding.
- Text subtitle tracks and external SRT, WebVTT and ASS files as timed cues.
- Hardware decoding through VideoToolbox, D3D11VA and VAAPI, falling back to software.
- `tools/ffmpeg` builds FFmpeg 9's LGPL shared libraries for each desktop into `dist/ffmpeg`.
- `go tool nsgo-media bundle` puts those libraries into the packages `nsgo build` wrote, signing a macOS app again.
- A demo app with its own controls, among them stepping both ways and saving a snapshot.
