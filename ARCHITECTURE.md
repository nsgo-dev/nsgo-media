# NSGo Media architecture

NSGo Media is a Go module that NSGo apps import. A player reads a source with FFmpeg on its own workers. It decodes audio into the system's output and video into frames that a view draws at the display's pace. The app owns every control and reads the player's state as NSGo signals. The [roadmap](https://github.com/oneo-me/nsgo-media/issues/2), whose sub-issues are its stages, theirs the tasks by area and theirs the subtasks, orders what remains; the website shows it.

## Code map

| Package | Responsibility |
| --- | --- |
| `player` | The public API: a `Player` opened on a source, its commands, its state as signals, tracks and subtitle cues, and `View`, the component that draws its picture |
| `internal/ffmpeg` | The binding of FFmpeg's C API through purego, checked against the library's version as it loads |
| `internal/ffmpeg/library` | Finding the FFmpeg libraries: an environment variable, the app's package, beside the executable, then the system's; on iOS the app's frameworks alone. Its `nsgo-natives.yaml` declares the published desktop libraries, which `nsgo build` puts into an app's packages |
| `internal/pipeline` | HTTP and HLS and DASH resources read in Go, choosing the variant, demuxing, bounded packet queues, decoders and their hardware fallback, the frame queue, the history for stepping backward, snapshots and the clock |
| `internal/audioout` | Audio output, one backend per target: CoreAudio on macOS, WASAPI on Windows, PulseAudio on Linux, each with PCM and an IEC 61937 stream for passthrough; CoreAudio's AudioQueue for PCM on iOS, and what iOS does to the output under the app |
| `cmd/nsgo-media` | `bundle`, which puts the frameworks into an iOS app `nsgo build` wrote and signs it again |
| `tools/ffmpeg` | The command that builds FFmpeg's LGPL shared libraries for each desktop target, and frameworks for iOS, at the pinned version, and writes the files of the release the desktop ones are published in and the manifest declaring them |
| `internal/stats` | Counting the demo's and NSGo Player's use in the website's Umami, tagged `demo` and `app` beside the site's `website`: a pageview per page and heartbeats while in use, with a random install ID; `nsgo dev` builds and tests send nothing |
| `examples/demo` | A player app with its own controls, showing how an app drives the core |
| `app` | NSGo Player, the first-party desktop app on plugins, a Go module of its own ([NSGo Player](docs/app.md)) |
| `website` | The official site, Astro with Starlight, whose docs it reads from `docs/public/<language>/` and GitHub Pages serves at media.nsgo.dev |

## Boundaries

- Import only NSGo's public packages, never its `internal/`, so the core works with any NSGo release it supports. On iOS that leaves `AVAudioSession` to NSGo: the core never sets its category or activates it, and only observes its notifications.
- Never use cgo or write C. FFmpeg and audio systems are reached through purego or `syscall`.
- Load only the FFmpeg major versions the binding names; refuse others instead of guessing struct layouts.
- Keep FFmpeg's GPL and nonfree components out of the libraries, so apps link the core under the LGPL.
- The reactive graph belongs to NSGo's engine goroutine. Workers reach it only through `Scope.Post`, and never block on it.
- Draw the picture through `core.Image`'s pixel buffers, so app views drawn after it sit above it on every desktop. A native child window would cover them on Windows.
- Keep every queue bounded by packets, bytes and frames per player, so a slow consumer stops reading instead of growing memory.
- Make `Close` stop and join every worker before it returns. No callback or signal update follows it.
- Leave the app's look to the app: the core draws only the picture and ships no controls, strings or themes.

## Core flows

**Open.** `Open` starts a goroutine that opens the input, reads its streams and posts the source's information. Files go through FFmpeg's file protocol; HTTP(S) is read in Go (`httpSource`, ranged GETs sending the source's headers) through a custom `AVIOContext`. An HLS or DASH manifest, known by its media type or extension, opens FFmpeg's demuxer for it, whose playlists, segments and keys Go opens as well with the same headers ([adaptive streams](docs/ffmpeg.md#adaptive-streams)); each segment downloads ahead of the demuxer into at most 2 MiB, timed for the bandwidth estimate. An external audio source, as DASH serves one, is a second input on the same timeline with its own demuxer: its audio tracks follow the source's, its first is chosen, and it feeds only the audio queue. Each demuxer carries out every seek; the buffered range is the lesser of the inputs being read. Decoders for the chosen tracks start, video on the platform's hardware device first.

**Play.** The demuxer fills a packet queue per stream and waits while they hold 2 s and 25 packets each, or 48 MiB together. The audio decoder resamples to the device's format, slows or speeds it with `atempo`, and queues 300 ms for the device. Each device frame it fills is recorded against the media time it plays, so the device's played count is the master clock, eased between the device's updates; without audio a monotonic clock stands in. The video decoder copies hardware frames to memory, converts them to RGBA no larger than the largest view needs, rotates them, and queues 3 in a pool of 7. Each frame keeps its decoded picture, which a snapshot converts again at the source's size.

**Present.** At every `RequestAnimationFrame` the player runs the presenter: it starts the clock once the first frame and 100 ms of sound are ready, shows the newest frame due by the clock (a few milliseconds early, a lead that shifts when two frames fall due in one display frame) and drops late ones. It sets the frame into the view's image source with a new version; the frame shown before goes back to the pool a display frame later, after the commit that copied it ran.

**Subtitles.** The demuxer decodes subtitle packets as it reads them: every text track's into cues kept for the source, and only the chosen bitmap track's into RGBA bitmaps, turned as the picture is, kept within 30 s of the position and 64 MiB. A bitmap cue ends where the next of its track starts; one whose end is not read yet, as PGS', shows only within the span read without a break since it, so a seek past that span never shows it. A VobSub file's packets stay in memory and decode as they show.

**Variants.** An HLS or DASH stream's variants are its video tracks with a bandwidth. Unless the app chose one, every 2 s of playing the highest that fits 80% of the estimated bandwidth is weighed: a lower one is taken at once, a higher one 10 s after the last change. Changing the variant discards the other tracks, plays the variant's own audio when its audio is muxed with it, and seeks exactly to the position, where the decoders reopen for the new tracks. A live stream that cannot seek starts the new variant where the demuxer reads.

**Passthrough.** How an audio track plays is decided at its first packet after it opens or is chosen again, and again when the rate leaves or reaches 1× or passthrough changes, which start its sound again from the position. A track of a codec passthrough is on for, at 1×, and for DTS with no extension substream or core extension in its packet or profile, closes the PCM device and opens one for its IEC 61937 stream: WASAPI's exclusive mode with the codec's format, a CoreAudio device hogged with its mixing off and an encoded physical format (only digital outputs list one, so PCM-only outputs such as HDMI ports refuse), or a PulseAudio stream of the passthrough encoding through the asynchronous API, which PipeWire's server answers too. DTS-HD (a core followed by an extension substream, or a High Resolution or Master Audio profile) and TrueHD take HDMI's high bit rate instead, 8 channels at 192 kHz (176.4 kHz for 44.1 kHz's family): WASAPI's DTS_HD and DOLBY_MLP formats, an encoded CoreAudio format with 8 channels at that rate, or PulseAudio 13's TrueHD and DTS-HD encodings. Where it fails, DTS-HD tries its core's stream when DTS passes, then PCM opens again, and the first error is kept. FFmpeg's spdif muxer wraps each packet: E-AC-3 gathers six blocks a burst, TrueHD 24 access units a MAT frame, and DTS-HD's whole frames go as DTS type IV bursts at a `dtshd_rate` of 768 kHz; a burst is one chunk of the ring at the carrier's rate and channels, so the device's played count stays the clock. The ring sends a burst whole even across a seek, and pause bursts in every gap; muted or at no volume, a burst's time passes in pause bursts. The device keeps running while paused, sending pause bursts, so the receiver stays locked.

**iOS.** The libraries are frameworks in the app, and PCM plays through the AudioQueue macOS uses, whose timeline an interruption starts again from zero, so `Played` adds the timelines up. The player watches the session's notifications: an interruption beginning pauses it and its end plays it again when the system says it should resume and the interruption made the pause; an old device becoming unavailable, as headphones unplugged, and the app's lifecycle going to the background pause it until the app plays. There is no passthrough. The session keeps the category the app or NSGo gave it ([nsgo#1931](https://github.com/nsgo-dev/nsgo/issues/1931) asks NSGo to let the core hold it).

**Seek.** A seek raises the pipeline's generation, flushes the queues and holds the clock at the target. Packets, frames and samples of an older generation are dropped wherever they are met, so a stale frame never shows. An exact seek drops what decodes before the target; a fast one starts at the keyframe. A hardware decoder that fails gives way to software and seeks again to the shown position.

**Step.** While paused, a step forward shows the next queued frame. A step backward shows the previous frame from the history: decoded pictures, in memory, of an unbroken run of frames before the shown one, at most 48 frames or 128 MiB. On a miss the video decoder seeks to a window before the shown frame, a few frames by the average rate, and keeps the frames from it up to the shown one; a window that held none is read again twice as long, and one reaching the source's start ends stepping there. Frames are chosen by their decoded timestamps, so variable rates step exactly. Steps asked for before the last ones showed add up. Resuming after a step backward seeks exactly to the frame shown, so sound and picture start there; seeking and resuming clear the history.

## Data and state

The core persists nothing. A player's state lives in memory until `Close`; the app stores positions, history or settings itself.

## Open questions

None now.

## Verification

Pipeline and player tests decode the clips in `testdata/` headless, through a silent output paced as a device, and check order, seeking, steps both ways at constant and variable rates, snapshots, rate, looping, text and bitmap subtitles (their times, clears, seeking past an unread end, and placement), HTTP with its headers, external audio in sync and `Close`. HLS and DASH fixtures, served by a local HTTP server, check seeking, a live sliding window, changing the variant, byte ranges, AES-128 and `Close` cancelling a segment's request; DASH's are skipped with libraries that lack it. `internal/audioout`'s test plays a tone on the real device and opens it for AC-3 passthrough. Passthrough tests send `passthrough.mkv`'s AC-3, E-AC-3 and DTS tracks and `hbr.mkv`'s DTS-HD Master Audio and TrueHD to a mock endpoint and read its stream back as bursts, through muting, seeking, the rate, changing the track and `Close`, and check every fallback; the packer's tests check DTS type IV bursts and MAT frames, their codes and the access units through them. The fixtures' DTS-HD is a real core with an extension substream of padding, and their TrueHD FFmpeg's experimental encoder's, with no Atmos. On real devices (2026-11-22), WASAPI's exclusive mode took AC-3 and DTS on a Windows 11 PC's S/PDIF output and refused E-AC-3, DTS-HD and TrueHD, DTS-HD then passing its core; the built-in output on macOS refused AC-3 and the high bit rate; an HDMI sink of PipeWire 1.6 without passthrough formats refused them all, and with AC-3 and DTS set took both and refused the high bit rate, DTS-HD passing its core; each refusal played PCM in sync. No receiver has decoded the bursts yet, nor has a CoreAudio encoded format, E-AC-3's 192 kHz carrier or a device taking the high bit rate been tried. On the iOS simulator, `go tool nsgo test -target ios_simulator -env NSGO_MEDIA_FFMPEG=<unpacked ios_simulator archive> -env NSGO_MEDIA_TESTDATA=$PWD/testdata` runs the `audioout`, `ffmpeg`, `pipeline` and `player` tests, DASH among them, with VideoToolbox decoding. On a desktop, `go tool nsgo test -target macos_arm64 -env NSGO_MEDIA_TESTDATA=$PWD/testdata` runs them, DASH among them, with the libraries `nsgo-natives.yaml` declares. `nsgo build` with installers gave universal NSGo Player and demo apps and disk images holding both architectures' libraries, signed ad hoc, and the demo from its disk image loaded them from its Frameworks as it opened a clip (2026-11-24); a Developer ID signature and notarization are untried. The demo's tests run headless, one as a phone, since a playing picture keeps a target's steps from settling. On an iPhone (2026-11-22) the demo played a local file and an HLS stream on VideoToolbox, with an AudioQueue's clock, seeking, steps both ways and subtitles, and paused as the phone locked; no interruption or unplugged headphones has been tried there. `nsgotest` drives the demo app. `TestMeasurePlayback` plays a long clip on a real machine; the results are in [Performance](docs/performance.md).

## Subjects

| Document | Read before changing |
| --- | --- |
| [Requirements](docs/requirements.md) | What the core includes, aims at or leaves out |
| [Roadmap](https://github.com/oneo-me/nsgo-media/issues/2) (issues) | The order of milestones or what comes after the first version |
| [Performance](docs/performance.md) | The presenter, the frame conversion or anything on the per-frame path |
| [NSGo Player](docs/app.md) | The first-party app in `app/` |
| [Plugins](docs/plugins.md) | NSGo Player's plugin manifest, exports, host object or sandbox |
| [FFmpeg libraries](docs/ffmpeg.md) | The pinned FFmpeg, its configure flags, the binding's majors or where libraries load from |
