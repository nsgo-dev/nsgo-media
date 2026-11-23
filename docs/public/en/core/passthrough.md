---
title: Audio passthrough
description: Send AC-3, E-AC-3, DTS, DTS-HD and TrueHD undecoded to a receiver, and tell what plays.
---

Passthrough sends a track's audio undecoded, in IEC 61937 bursts, to the receiver the output device drives, such as an amplifier on HDMI or S/PDIF, which decodes it. It is off unless the app sets it, and iOS offers it to no app.

```go
p.SetPassthrough(player.PassthroughAC3 | player.PassthroughEAC3 | player.PassthroughDTS |
	player.PassthroughDTSHD | player.PassthroughTrueHD)
p.SetPassthrough(0) // off
```

| Codec | Passes |
| --- | --- |
| `PassthroughAC3`, `PassthroughEAC3` | AC-3 and E-AC-3 |
| `PassthroughDTS` | DTS' core, and DTS-HD's core where `PassthroughDTSHD` is not set or the device takes no high bit rate |
| `PassthroughDTSHD` | DTS-HD High Resolution and Master Audio whole, on HDMI's high bit rate carrier |
| `PassthroughTrueHD` | TrueHD as its stream is, Atmos and all, on HDMI's high bit rate carrier |

The high bit rate carrier is 8 channels at 192 kHz, or 176.4 kHz for 44.1 kHz's family.

## Per platform

| Platform | Through |
| --- | --- |
| Windows | WASAPI in exclusive mode |
| macOS | A hogged CoreAudio device with an encoded format |
| Linux | A PulseAudio sink with passthrough formats, which PipeWire's server answers too |

## When PCM plays instead

A track of another codec plays as PCM, and so does one passthrough is set for when:

- the rate is other than 1×,
- it is enhanced DTS that does not pass, such as DTS-ES, DTS 96/24, DTS Express or DTS-HD without `PassthroughDTSHD`,
- the device or the system does not take the stream, refuses it or fails to start it. DTS-HD then passes its core where `PassthroughDTS` is set.

`p.AudioOutput()` tells what plays now:

```go
o := p.AudioOutput()
if o.Passthrough {
	fmt.Println("passing", o.Codec, "at", o.SampleRate, "Hz,", o.Channels, "channels")
} else if o.Fallback != player.FallbackNone {
	fmt.Println("PCM because of", o.Fallback, o.Err) // rate, enhanced DTS or device
}
```

`SampleRate` and `Channels` are the device stream's: the PCM's or the carrier's. The track's own are in its `Track`.

## While passing through

Volume does not change the sound, and muting sends silence the receiver understands. The device's played count stays the clock through seeking and changing the track, and pausing sends pause bursts so the receiver stays locked. Changing passthrough while a track of its codecs plays starts its sound again from the position.

:::caution[Tried so far]
On a Windows 11 PC's S/PDIF output, WASAPI took AC-3 and DTS. No receiver has decoded the bursts yet, nor has a device taking the high bit rate been tried.
:::
