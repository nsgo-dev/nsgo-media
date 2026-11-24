---
title: Playback
description: Open a source, drive the player from your controls, read its state and place its picture.
---

A `player.Player` plays one source at a time. It belongs to the scope that made it and to its engine's goroutine: call its methods from components, effects and event handlers. It decodes on its own goroutines.

## Make a player

```go
p := player.New(cx)                    // closed when cx is disposed
p := player.New(cx, player.Software()) // with hardware decoding off
```

`Close` stops playback and every goroutine of the player, and returns once they have exited; no signal changes after it. The player may open another source.

## Open a source

`Open` takes a file path or an http(s) URL and closes what was open.

```go
p.Open("https://cdn.example/movie.mp4",
	player.Headers(http.Header{
		"Referer":    {"https://example.com/"},
		"User-Agent": {"MyApp/1.0"},
	}),
)
```

- `player.Headers(h)` sends `h` with every HTTP request for the source, such as the `Referer`, `User-Agent` and `Cookie` a CDN asks for.
- `player.ExternalAudio(source, h)` plays the audio of another source with the picture, as DASH sites serve them apart. Its audio tracks follow the source's, marked `External`, and its first plays. Seeking, rate, pausing and stepping move both sources together.

Once open, `p.Info()` gives the duration, the container's format, bit rate and metadata, the tracks with their codecs, languages and picture sizes, and the chapters.

## Commands

| Method | Does |
| --- | --- |
| `Play()` | Plays, from the start when it ended |
| `Pause()` | Pauses |
| `Stop()` | Pauses at the start |
| `Seek(t, player.Fast)` | Lands on the keyframe at or before `t`, which shows at once |
| `Seek(t, player.Exact)` | Lands on the frame showing at `t`, decoding from the keyframe before it |
| `StepForward()` | Shows the next frame while paused |
| `StepBackward()` | Shows the previous frame while paused; playing again goes on from it, sound included |
| `SetRate(r)` | Sets the speed from 0.25 to 4, keeping the pitch |
| `SetVolume(v)`, `SetMuted(m)` | Sets the volume from 0 to 1, or silences the sound |
| `SetLoop(l)` | Starts over at the end |
| `SelectAudio(id)` | Plays the audio track `id`, or no sound for -1 |
| `Snapshot(w, h)` | Gives the frame showing as an `*image.RGBA`, at the source's size or shrunk to fit `w` by `h` |

Steps asked for before the last ones showed add up. Recent frames stay decoded, so stepping back over them is quick; older ones decode again from the keyframe before them.

## State

Every getter reads an NSGo signal, so a view that reads it follows it.

| Getter | Is |
| --- | --- |
| `State()` | `Idle`, `Opening`, `Paused`, `Playing`, `Buffering`, `Ended` or `Failed` |
| `Err()` | Why the player failed, nil unless `Failed` |
| `Info()` | The source's information, nil until it opened |
| `Duration()`, `Position()` | The duration, 0 when unknown, and the position, updated at every display frame |
| `Buffered()` | How far from the start the source is read |
| `AudioTrack()`, `VideoTrack()`, `SubtitleTrack()` | The playing tracks' IDs, -1 for none |
| `Rate()`, `Volume()`, `Muted()`, `Loop()` | What the setters last set |
| `HardwareDevice()` | `"videotoolbox"`, `"d3d11va"` or `"vaapi"`, or empty for software decoding |
| `VideoSize()` | The size the video shows at, its pixels' shape and rotation applied |

```go
core.Text{ContentFunc: func() string {
	if p.State() == player.Failed {
		return p.Err().Error()
	}
	return p.Position().Truncate(time.Second).String()
}}
```

## The view

`player.View` draws the picture in its box. Its children draw above the picture, as do views placed after it: the picture is an ordinary image view, or on Windows and Linux a GPU view for hardware-decoded pictures, never a native window.

```go
player.View{
	Player: p,
	Fit:    player.Contain, // or player.Cover, player.Fill
	Children: []core.Node{ /* your controls and overlays */ },
}
```

`Contain` shows the whole picture with bars, `Cover` fills the view and crops what overflows, and `Fill` stretches the picture. `FitFunc` gives the fit from a signal instead. The view follows the stream's rotation, and the frames it gets are no larger than the largest view needs.

## On iOS

While a player plays, it holds the app's audio session at Playback through NSGo, so the Ring/Silent switch does not silence it, and other apps' audio pauses. It pauses as an interruption, such as a call or Siri, begins, and plays again as it ends if the system allows it. It pauses as headphones are unplugged, and stays paused until the app plays.

To keep playing in the background and with the screen locked, declare the `audio` background mode in `nsgo.yaml`:

```yaml
apple:
  info:
    UIBackgroundModes: [audio]
```

The sound then plays on there, and the picture comes back where it plays as the app returns; a source without sound pauses. Without the mode, a player pauses as the app goes to the background and stays paused until the app plays. The core shows no lock-screen or Control Center controls.
