# NSGo Media architecture

NSGo Media is a Go module that NSGo apps import. A player reads a source with FFmpeg on its own workers. It decodes audio into the system's output and video into frames that a view draws at the display's pace. The app owns every control and reads the player's state as NSGo signals. Nothing is built yet: this is the planned design, and the [roadmap](docs/roadmap.md) orders it.

## Code map

| Package | Responsibility |
| --- | --- |
| `player` | The public API: a `Player` opened on a source, its commands, its state as signals, tracks and subtitle cues, and `View`, the component that draws its picture |
| `internal/ffmpeg` | The binding of FFmpeg's C API through purego, checked against the library's version as it loads |
| `internal/ffmpeg/library` | Finding the FFmpeg libraries: an environment variable, the app's package, beside the executable, then the system's |
| `internal/pipeline` | Demuxing, bounded packet queues, decoders and their hardware fallback, the frame queue and the clock |
| `internal/audioout` | Audio output, one backend per target: CoreAudio on macOS, WASAPI on Windows, PulseAudio on Linux |
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

**Open.** `Open` starts a worker that opens the input, reads its streams and posts the source's information. Decoders for the chosen tracks start, hardware first where the platform offers it.

**Play.** The demuxer fills a packet queue per stream. The audio decoder resamples into the output device's buffer, whose played position is the master clock; without audio a monotonic clock stands in. The video decoder converts frames to RGBA into a small pool.

**Present.** On each `RequestAnimationFrame` the view takes the newest frame due by the clock and drops the late ones. It writes that frame into the image's pixel buffer with a new version. Decoding stays on its workers, so the engine goroutine only copies.

**Seek.** A seek raises the pipeline's generation and flushes the queues. Packets, frames and cues of an older generation are dropped wherever they are met, so a stale frame never shows after a seek.

## Data and state

The core persists nothing. A player's state lives in memory until `Close`; the app stores positions, history or settings itself.

## Open questions

- **Packaging the libraries.** How do FFmpeg's libraries get into an app's package, signed into a macOS app's `Frameworks` and beside a Windows or Linux executable? This needs support from the `nsgo` tool or a step of the core's own, chosen with the user.

## Verification

Pipeline tests decode small fixture clips in `testdata/` headless and check timestamps, seeking and frame steps. `nsgotest` drives the demo app on each desktop. Sound output, sync and 4K performance need a check on real machines.

## Subjects

| Document | Read before changing |
| --- | --- |
| [Requirements](docs/requirements.md) | What the first version includes or leaves out |
| [Roadmap](docs/roadmap.md) | The order of milestones or what comes after the first version |
| [FFmpeg libraries](docs/ffmpeg.md) | The pinned FFmpeg, its configure flags, the binding's majors or where libraries load from |
