<h1 align="center">NSGo Media</h1>

<p align="center">
  <strong>简体中文</strong> |
  <a href="README.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/oneo-me"><img alt="GitHub" src="https://img.shields.io/badge/GitHub-oneo--me-181717?logo=github&logoColor=white"></a>
  <a href="https://oneo.me"><img alt="博客" src="https://img.shields.io/badge/%E5%8D%9A%E5%AE%A2-oneo.me-0969da"></a>
  <a href="https://x.com/oneo666"><img alt="X" src="https://img.shields.io/badge/X-%40oneo666-000000?logo=x&logoColor=white"></a>
  <a href="https://github.com/sponsors/oneo-me"><img alt="GitHub 赞助" src="https://img.shields.io/badge/GitHub%20%E8%B5%9E%E5%8A%A9-ea4aaa?logo=githubsponsors&logoColor=white"></a>
</p>

<p align="center">
  面向 <a href="https://github.com/nsgo-dev/nsgo">NSGo</a> 应用的视频播放器内核：用 FFmpeg 解码，同步声音和画面，并在视图中绘制画面；控制界面由应用自己实现。
</p>

## 状态

开发中：播放、声音、定位、字幕和硬件解码已在 macOS、Windows 和 Linux 上可供本地使用，不发布线上版本。[需求](docs/requirements.md)定义了首个版本，[路线图](docs/roadmap.md)记录了之后的工作。

## 用法

应用创建播放器，放置它的视图，并在自己的控件里调用它的方法：

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

播放器的状态（如 `p.State()`、`p.Position()` 和 `p.Cues()`）是 NSGo 信号，应用的视图直接读取。[`examples/demo`](examples/demo) 是一个带完整自定义控件的播放器。

内核在运行时加载 FFmpeg 动态库：优先使用 `tools/ffmpeg` 构建的库，从 `NSGO_MEDIA_FFMPEG` 指定的目录或可执行文件旁加载，否则使用系统的 FFmpeg 9（macOS 上可 `brew install ffmpeg`）。应用的包在 `nsgo build` 之后用 `go tool nsgo-media bundle` 放入这些库。详见 [FFmpeg 库](docs/ffmpeg.md)。

## 开发

安装 [Go](https://go.dev/dl/) 1.27.3 或更新版本后，在仓库根目录运行：

```sh
go tool nsgo dev examples/demo  # 以热重载运行 demo 应用
go tool nsgo check              # 检查所有包
go tool nsgo test               # 运行所有包的测试
```

设计见 [ARCHITECTURE.md](ARCHITECTURE.md)，参与方式见 [AGENTS.md](AGENTS.md)。

## 许可证

本项目以 [MIT 许可证](LICENSE) 授权。项目构建的 FFmpeg 库以 LGPL 2.1 或更新版本授权。

除非你明确声明，否则你提交的任何贡献均以 MIT 许可证授权，不附加任何额外条款或条件。
