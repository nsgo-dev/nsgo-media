# NSGo Media architecture

NSGo Media is a Go module that NSGo apps import. A player reads a source with FFmpeg on its own workers. It decodes audio into the system's output and video into frames that a view draws at the display's pace. The app owns every control and reads the player's state as NSGo signals. The [roadmap](docs/roadmap.md) orders what remains.

## Code map

| Package | Responsibility |
| --- | --- |
| `player` | The public API: a `Player` opened on a source, its commands, its state as signals, tracks and subtitle cues, and `View`, the component that draws its picture |
| `internal/ffmpeg` | The binding of FFmpeg's C API through purego, checked against the library's version as it loads |
| `internal/ffmpeg/library` | Finding the FFmpeg libraries: an environment variable, the app's package, beside the executable, then the system's |
| `internal/pipeline` | Demuxing, bounded packet queues, decoders and their hardware fallback, the frame queue, the history for stepping backward, snapshots and the clock |
| `internal/audioout` | Audio output, one backend per target: CoreAudio on macOS, WASAPI on Windows, PulseAudio on Linux |
| `cmd/nsgo-media` | `bundle`, which puts the libraries into the packages `nsgo build` wrote and signs a macOS app again |
| `tools/ffmpeg` | The command that builds FFmpeg's LGPL shared libraries for each desktop target at the pinned version |
| `examples/demo` | A player app with its own controls, showing how an app drives the core |

## Boundaries

- Import only NSGo's public packages, never its `internal/`, so the core works with any NSGo release it supports.
- Never use cgo or write C. FFmpeg and audio systems are reached through purego or `syscall`.
- Load only the FFmpeg major versions the binding names; refuse others instead of guessing struct layouts.
- Keep FFmpeg's GPL and nonfree components out of the libraries, so apps link the core under the LGPL.
- The reactive graph belongs to NSGo's engine goroutine. Workers reach it only through `Scope.Post`, and never block on it.
- Draw the picture through `core.Image`'s pixel buffers, so app views drawn after it sit above it on every desktop. A native child window would cover them on Windows.
- Keep every queue bounded by packets, bytes and frames per player, so a slow consumer stops reading instead of growing memory.
- Make `Close` stop and join every worker before it returns. No callback or signal update follows it.
- Leave the app's look to the app: the core draws only the picture and ships no controls, strings or themes.

## Core flows

**Open.** `Open` starts a goroutine that opens the input, reads its streams and posts the source's information. Files go through FFmpeg's file protocol; HTTP(S) is read in Go (`httpSource`, ranged GETs) through a custom `AVIOContext`. Decoders for the chosen tracks start, video on the platform's hardware device first.

**Play.** The demuxer fills a packet queue per stream and waits while they hold 2 s and 25 packets each, or 48 MiB together. The audio decoder resamples to the device's format, slows or speeds it with `atempo`, and queues 300 ms for the device. Each device frame it fills is recorded against the media time it plays, so the device's played count is the master clock, eased between the device's updates; without audio a monotonic clock stands in. The video decoder copies hardware frames to memory, converts them to RGBA no larger than the largest view needs, rotates them, and queues 3 in a pool of 7. Each frame keeps its decoded picture, which a snapshot converts again at the source's size.

**Present.** At every `RequestAnimationFrame` the player runs the presenter: it starts the clock once the first frame and 100 ms of sound are ready, shows the newest frame due by the clock (a few milliseconds early, a lead that shifts when two frames fall due in one display frame) and drops late ones. It sets the frame into the view's image source with a new version; the frame shown before goes back to the pool a display frame later, after the commit that copied it ran.

**Seek.** A seek raises the pipeline's generation, flushes the queues and holds the clock at the target. Packets, frames and samples of an older generation are dropped wherever they are met, so a stale frame never shows. An exact seek drops what decodes before the target; a fast one starts at the keyframe. A hardware decoder that fails gives way to software and seeks again to the shown position.

**Step.** While paused, a step forward shows the next queued frame. A step backward shows the previous frame from the history: decoded pictures, in memory, of an unbroken run of frames before the shown one, at most 48 frames or 128 MiB. On a miss the video decoder seeks to a window before the shown frame, a few frames by the average rate, and keeps the frames from it up to the shown one; a window that held none is read again twice as long, and one reaching the source's start ends stepping there. Frames are chosen by their decoded timestamps, so variable rates step exactly. Steps asked for before the last ones showed add up. Resuming after a step backward seeks exactly to the frame shown, so sound and picture start there; seeking and resuming clear the history.

## Data and state

The core persists nothing. A player's state lives in memory until `Close`; the app stores positions, history or settings itself.

## Open questions

None now. Packaging the libraries is a step of the core's own after `nsgo build` (decided by the user, 2026-11-22; [FFmpeg libraries](docs/ffmpeg.md#packaging-an-app)); the `nsgo` tool carrying third-party packages' native libraries itself is proposed to NSGo.

## Verification

Pipeline and player tests decode the clips in `testdata/` headless, through a silent output paced as a device, and check order, seeking, steps both ways at constant and variable rates, snapshots, rate, looping, subtitles, HTTP and `Close`. `internal/audioout`'s test plays a tone on the real device. `nsgotest` drives the demo app. `TestMeasurePlayback` plays a long clip on a real machine; the results are in [Performance](docs/performance.md).

## Subjects

| Document | Read before changing |
| --- | --- |
| [Requirements](docs/requirements.md) | What the first version includes or leaves out |
| [Roadmap](docs/roadmap.md) | The order of milestones or what comes after the first version |
| [Performance](docs/performance.md) | The presenter, the frame conversion or anything on the per-frame path |
| [FFmpeg libraries](docs/ffmpeg.md) | The pinned FFmpeg, its configure flags, the binding's majors or where libraries load from |
