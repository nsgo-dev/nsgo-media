---
title: 播放
description: 打开媒体源，从你的控件驱动播放器，读取它的状态，放好它的画面。
---

一个 `player.Player` 一次播放一个媒体源。它属于创建它的作用域和该引擎的 goroutine：在组件、effect 和事件处理函数里调用它的方法。它在自己的 goroutine 上解码。

## 创建播放器

```go
p := player.New(cx)                    // cx 销毁时关闭
p := player.New(cx, player.Software()) // 关闭硬件解码
```

`Close` 停止播放和播放器的所有 goroutine，等它们都退出后才返回；之后不再有信号变化。播放器还能再打开别的媒体源。

## 打开媒体源

`Open` 接受文件路径或 http(s) 地址，并关闭之前打开的。

```go
p.Open("https://cdn.example/movie.mp4",
	player.Headers(http.Header{
		"Referer":    {"https://example.com/"},
		"User-Agent": {"MyApp/1.0"},
	}),
)
```

- `player.Headers(h)` 在该媒体源的每个 HTTP 请求里带上 `h`，比如 CDN 要求的 `Referer`、`User-Agent` 和 `Cookie`。
- `player.ExternalAudio(source, h)` 把另一个媒体源的音频和画面一起播放，就像 DASH 站点把它们分开提供那样。它的音轨排在媒体源的音轨之后，标记为 `External`，默认播放第一条。跳转、变速、暂停和逐帧会同时作用于两个媒体源。

打开之后，`p.Info()` 给出时长、容器格式、码率和元数据，带编码、语言和画面尺寸的轨道列表，以及章节。

## 命令

| 方法 | 作用 |
| --- | --- |
| `Play()` | 播放；已结束时从头播放 |
| `Pause()` | 暂停 |
| `Stop()` | 暂停并回到开头 |
| `Seek(t, player.Fast)` | 落在 `t` 处或之前的关键帧上，立即显示 |
| `Seek(t, player.Exact)` | 落在 `t` 时刻显示的那一帧上，从它之前的关键帧开始解码 |
| `StepForward()` | 暂停时显示下一帧 |
| `StepBackward()` | 暂停时显示上一帧；再次播放从这一帧继续，声音也一样 |
| `SetRate(r)` | 设置 0.25 到 4 的速度，保持音调 |
| `SetVolume(v)`、`SetMuted(m)` | 设置 0 到 1 的音量，或静音 |
| `SetLoop(l)` | 播到结尾时从头开始 |
| `SelectAudio(id)` | 播放音轨 `id`，-1 表示无声 |
| `Snapshot(w, h)` | 以 `*image.RGBA` 返回当前显示的帧，保持原尺寸，或缩小到 `w` × `h` 以内 |

上一次逐帧还没显示时又请求的逐帧会累加。最近的帧保持解码状态，所以往回逐帧很快；更早的帧会从之前的关键帧重新解码。

## 状态

每个 getter 读取的都是 NSGo 信号，读取它的视图会随之更新。

| Getter | 含义 |
| --- | --- |
| `State()` | `Idle`、`Opening`、`Paused`、`Playing`、`Buffering`、`Ended` 或 `Failed` |
| `Err()` | 播放器失败的原因，不是 `Failed` 时为 nil |
| `Info()` | 媒体源的信息，打开前为 nil |
| `Duration()`、`Position()` | 时长（未知时为 0）和播放位置（每个显示帧更新） |
| `Buffered()` | 媒体源从开头读到了哪里 |
| `AudioTrack()`、`VideoTrack()`、`SubtitleTrack()` | 正在播放的轨道 ID，没有时为 -1 |
| `Rate()`、`Volume()`、`Muted()`、`Loop()` | 对应 setter 最后设置的值 |
| `HardwareDevice()` | `"videotoolbox"`、`"d3d11va"` 或 `"vaapi"`，软件解码时为空 |
| `VideoSize()` | 视频显示的尺寸，已计入像素宽高比和旋转 |

```go
core.Text{ContentFunc: func() string {
	if p.State() == player.Failed {
		return p.Err().Error()
	}
	return p.Position().Truncate(time.Second).String()
}}
```

## 视图

`player.View` 在自己的区域里绘制画面。它的子节点画在画面之上，排在它后面的视图也一样：画面在每个桌面平台上都是普通的图像视图，而不是原生窗口。

```go
player.View{
	Player: p,
	Fit:    player.Contain, // 或 player.Cover、player.Fill
	Children: []core.Node{ /* 你的控件和浮层 */ },
}
```

`Contain` 完整显示画面并留出黑边，`Cover` 填满视图并裁掉溢出部分，`Fill` 拉伸画面。`FitFunc` 可以改从信号里取填充方式。视图会跟随流的旋转，它拿到的帧不会大于最大的视图所需的尺寸。

## 在 iOS 上

来电、Siri 等中断开始时播放器暂停，中断结束时如果系统允许就继续播放。拔掉耳机或应用进入后台时它会暂停，直到应用再次播放：它不在后台播放。内核从不设置音频会话的类别，所以在应用或 NSGo 设置别的类别之前，使用 iOS 的默认类别，静音开关会让它静音。
