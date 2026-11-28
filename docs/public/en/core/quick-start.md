---
title: Quick start
description: Add NSGo Media to an NSGo app, give it FFmpeg's libraries and open a first file.
---

You need [Go](https://go.dev/dl/) 1.27.3 or later and an [NSGo](https://github.com/nsgo-dev/nsgo) app.

## Add the module

In your app's directory:

```sh
go get github.com/nsgo-dev/nsgo-media@v0.1.0
```

It requires NSGo v0.4.0 or later, and moves your app onto it if it is older.

The module was `github.com/oneo-me/nsgo-media` before v0.1.0. If your app imports that path, change its imports to `github.com/nsgo-dev/nsgo-media/...`, drop the `require` and `replace` lines of the old path from `go.mod`, and run the `go get` above. An app pinned to a version of the old path keeps building.

## Get FFmpeg's libraries

The core loads FFmpeg 9's shared libraries at run time. On macOS, Windows and Linux, `nsgo dev` and `nsgo build` download the ones the repository publishes and put them into your app; you need nothing else.

To change them, or for iOS, build them from a clone of the repository, which runs on macOS with Xcode and Go alone:

```sh
git clone https://github.com/nsgo-dev/nsgo-media.git
cd nsgo-media
go run ./tools/ffmpeg                     # every target into dist/ffmpeg
go run ./tools/ffmpeg -target macos_arm64 # or one target
```

With `go run` or `go test`, which put no libraries in, you can also use the system's FFmpeg on macOS:

```sh
brew install ffmpeg
```

Homebrew's libraries have no libxml2, so they play HLS but not DASH. The core looks in the directory `NSGO_MEDIA_FFMPEG` names first; [FFmpeg libraries](../ffmpeg/#where-they-load-from) lists the rest.

## Open a file

Make a player in a component, open a source and place its view. The player belongs to the scope that made it and closes when the scope is disposed.

```go
import "github.com/nsgo-dev/nsgo-media/player"

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

[NSGo Player](../../), in `app/`, is the first-party desktop app built on the core: it plays your video folders through its built-in local files plugin, and other sources through plugins. It is a Go module of its own:

```sh
cd app && go tool nsgo dev
```

## Next

- [Playback](../playback/) for the player's commands, state and view.
- [FFmpeg libraries](../ffmpeg/) before you ship an app.
