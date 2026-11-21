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

Planning: nothing is built yet. The [requirements](docs/requirements.md) define the first version, for macOS, Windows and Linux, and the [roadmap](docs/roadmap.md) orders the work.

## Usage

An app opens a player, places its view and calls its methods from its own controls. This is the planned shape of the API:

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

## Development

With [Go](https://go.dev/dl/) 1.27.3 or later, from the repository root:

```sh
go tool nsgo dev examples/demo  # run the demo app with hot reload
go tool nsgo check              # vet every package
go tool nsgo test               # every package's tests
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the design and [AGENTS.md](AGENTS.md) for how to work on the project.

## License

Licensed under the [MIT License](LICENSE). The FFmpeg libraries the project builds are under the LGPL 2.1 or later.

Unless you explicitly state otherwise, any contribution you submit is licensed under the MIT License, without any additional terms or conditions.
