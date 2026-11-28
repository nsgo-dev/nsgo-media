<h1 align="center">NSGo Media</h1>

<p align="center">
  <strong>English</strong> |
  <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/oneo-me"><img alt="GitHub" src="https://img.shields.io/badge/GitHub-oneo--me-181717?logo=github&logoColor=white"></a>
  <a href="https://oneo.me"><img alt="Blog" src="https://img.shields.io/badge/Blog-oneo.me-0969da"></a>
  <a href="https://x.com/oneo666"><img alt="X" src="https://img.shields.io/badge/X-%40oneo666-000000?logo=x&logoColor=white"></a>
  <a href="https://github.com/sponsors/oneo-me"><img alt="GitHub Sponsors" src="https://img.shields.io/badge/GitHub%20Sponsors-ea4aaa?logo=githubsponsors&logoColor=white"></a>
</p>

<p align="center">
  A video player core for <a href="https://github.com/nsgo-dev/nsgo">NSGo</a> apps: it decodes with FFmpeg, keeps sound and picture in sync and draws the picture in a view, while the app builds its own controls.
</p>

## Status

In development: playback, sound, seeking, subtitles and hardware decoding work on macOS, Windows, Linux and iOS for local use. The core installs with `go get` from v0.1.0. [NSGo Player](docs/app.md) runs from a checkout and installs from the packages `nsgo build` writes on macOS, Windows and Linux, and the core serves what it needs; the [requirements](docs/requirements.md) record what the core does and the [roadmap](https://github.com/nsgo-dev/nsgo-media/issues/2) orders what comes next.

## Usage

An app opens a player, places its view and calls its methods from its own controls:

```go
func Root(cx *core.Scope) core.Node {
	p := player.New(cx)
	p.Open("movie.mkv")
	return core.View{Children: []core.Node{
		player.View{Player: p},
		ui.Button{Text: "Play", OnPress: p.Play},
	}}
}
```

The player's state, such as `p.State()`, `p.Position()` and `p.Cues()`, is NSGo signals the app's views read. [`examples/demo`](examples/demo) is a small player with its own controls, and [NSGo Player](docs/app.md), in `app/`, the first-party desktop app: local folders through its built-in plugin, and other sources through plugins with YAML manifests.

The core loads FFmpeg's shared libraries at run time: the ones `tools/ffmpeg` builds, from the directory `NSGO_MEDIA_FFMPEG` names, beside the executable or as frameworks in an iOS app, or else the system's FFmpeg 9 (`brew install ffmpeg` on macOS). `nsgo build` downloads them, published as a release of this repository, into an app's packages and installers, on iOS too. See [FFmpeg libraries](docs/ffmpeg.md).

## Development

With [Go](https://go.dev/dl/) 1.27.3 or later, from the repository root:

```sh
go tool nsgo dev examples/demo  # run the demo app with hot reload
go tool nsgo check              # vet every package
go tool nsgo test               # every package's tests
cd app && go tool nsgo dev      # run NSGo Player, a module of its own; check and test it there too
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the design and [AGENTS.md](AGENTS.md) for how to work on the project.

## License

Licensed under the [MIT License](LICENSE). The FFmpeg libraries the project builds are under the LGPL 2.1 or later; [FFmpeg libraries](docs/ffmpeg.md#what-an-app-that-ships-them-owes) lists what an app that ships them owes.

Unless you explicitly state otherwise, any contribution you submit is licensed under the MIT License, without any additional terms or conditions.
