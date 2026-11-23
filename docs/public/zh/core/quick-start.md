---
title: 快速开始
description: 把 NSGo Media 加进 NSGo 应用，准备好 FFmpeg 库，打开第一个文件。
---

你需要 [Go](https://go.dev/dl/) 1.27.3 或更高版本，以及一个 [NSGo](https://github.com/nsgo-dev/nsgo) 应用。

## 添加模块

目前还没有发布版本，所以使用本地检出。把存储库克隆到应用旁边，再让应用的 `go.mod` 指向它：

```sh
git clone https://github.com/oneo-me/nsgo-media.git
```

```go title="go.mod"
require github.com/oneo-me/nsgo-media v0.0.0

replace github.com/oneo-me/nsgo-media => ../nsgo-media
```

## 准备 FFmpeg 库

内核在运行时加载 FFmpeg 9 的共享库。在 macOS、Windows 和 Linux 上，`nsgo dev` 和 `nsgo build` 会下载存储库发布的库，放进应用，不需要别的准备。

要修改这些库，或者用于 iOS，就在检出目录里构建它们，只需要 macOS 上的 Xcode 和 Go：

```sh
cd nsgo-media
go run ./tools/ffmpeg                     # 构建所有目标到 dist/ffmpeg
go run ./tools/ffmpeg -target macos_arm64 # 或只构建一个目标
```

用 `go run` 或 `go test` 时不会放入库，在 macOS 上也可以使用系统的 FFmpeg：

```sh
brew install ffmpeg
```

Homebrew 的库没有 libxml2，所以能播 HLS，但不能播 DASH。内核会先在 `NSGO_MEDIA_FFMPEG` 指定的目录里找；其余位置见 [FFmpeg 库](../ffmpeg/#加载位置)。

## 打开文件

在组件里创建播放器，打开媒体源，再放好它的视图。播放器属于创建它的作用域，作用域销毁时随之关闭。

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

播放器在能显示第一帧之前处于 `Opening`，之后是 `Paused`，调用 `Play` 后是 `Playing`。它的状态，比如 `p.State()`、`p.Position()` 和 `p.Cues()`，都是你的视图可以读取的 NSGo 信号。

## 运行演示

存储库里的演示应用是一个带完整控件的播放器：双向逐帧、截图、选择音轨、清晰度和字幕、位图字幕，以及直通开关。在检出目录里运行：

```sh
go tool nsgo dev examples/demo
```

`app/` 里的 [NSGo Player](../../) 是基于内核的第一方桌面应用：用内置的本地文件插件播放你的视频文件夹，其他来源由插件提供。它是独立的 Go 模块：

```sh
cd app && go tool nsgo dev
```

## 下一步

- [播放](../playback/)：播放器的命令、状态和视图。
- 分发应用之前，先读 [FFmpeg 库](../ffmpeg/)。
