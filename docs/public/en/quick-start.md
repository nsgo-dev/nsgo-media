---
title: Quick start
description: Add NSGo Media to an NSGo app, give it FFmpeg's libraries and open a first file.
---

You need [Go](https://go.dev/dl/) 1.27.3 or later and an [NSGo](https://github.com/nsgo-dev/nsgo) app.

## Add the module

There is no published release, so use a local checkout. Clone the repository beside your app and point your app's `go.mod` at it:

```sh
git clone https://github.com/oneo-me/nsgo-media.git
```

```go title="go.mod"
require github.com/oneo-me/nsgo-media v0.0.0

replace github.com/oneo-me/nsgo-media => ../nsgo-media
```

## Get FFmpeg's libraries

The core loads FFmpeg 9's shared libraries at run time. Either build them from the checkout, which runs on macOS with Xcode and Go alone:

```sh
cd nsgo-media
go run ./tools/ffmpeg                     # every target into dist/ffmpeg
go run ./tools/ffmpeg -target macos_arm64 # or one target
```

or, while developing on macOS, use the system's FFmpeg:

```sh
brew install ffmpeg
```

Homebrew's libraries have no libxml2, so they play HLS but not DASH. The core looks in the directory `NSGO_MEDIA_FFMPEG` names first; [FFmpeg libraries](../ffmpeg/#where-they-load-from) lists the rest.

## Open a file

Make a player in a component, open a source and place its view. The player belongs to the scope that made it and closes when the scope is disposed.

```go
import "github.com/oneo-me/nsgo-media/player"

func Root(cx *core.Scope) core.Node {
	p := player.New(cx)
	p.Open("movie.mkv")
	return core.View{Children: []core.Node{
		player.View{Player: p},
		ui.Button{Text: "Play", OnPress: p.Play},
	}}
}
```

The player is `Opening` until it can show the first frame, then `Paused`, or `Playing` after `Play`. Its state, such as `p.State()`, `p.Position()` and `p.Cues()`, is NSGo signals your views read.

## Run the demo

The repository's demo is a complete player with its own controls: stepping both ways, snapshots, track, quality and subtitle choice, bitmap subtitles and a passthrough switch. From the checkout:

```sh
go tool nsgo dev examples/demo
```

[NSGo Player](../app/), in `app/`, is the first-party desktop app built on the core: it plays your video folders through its built-in local files plugin, and other sources through plugins. It is a Go module of its own:

```sh
cd app && go tool nsgo dev
```

## Next

- [Playback](../playback/) for the player's commands, state and view.
- [FFmpeg libraries](../ffmpeg/) before you ship an app.
