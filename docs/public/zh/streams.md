---
title: HLS 和 DASH
description: 播放点播和直播的自适应流，由带宽或应用选择清晰度。
---

`Open` 像播放文件一样播放 HLS 或 DASH 清单，按媒体类型或扩展名识别。它的播放列表、分片和密钥都在 Go 中读取，并带上 `player.Headers` 给出的请求头。

```go
p.Open("https://cdn.example/live/index.m3u8")
```

DASH 需要带 libxml2 构建的 FFmpeg 库，`tools/ffmpeg` 构建的就是这样。没有 libxml2 的库（比如 Homebrew 的）能播 HLS，但不能播 DASH。

## 直播

`Info.Live` 表示直播流，它不报告时长。只保留滑动窗口的直播流 `Info.Seekable` 为 false：`Seek` 会让它停在正在播放的地方。

## 清晰度

`Info.Variants()` 以视频轨道的形式列出流的各个清晰度，每个带有 `Bandwidth`。由带宽选择时，`AutoVariant()` 为 true：每播放 2 秒，内核就权衡能放进估算带宽 80% 的最高清晰度，更低的立即切换，更高的则在上次切换 10 秒后才切换。

`SelectVariant(id)` 从当前位置播放视频轨道为 `id` 的清晰度；传 -1 则重新交给带宽选择。切换清晰度会从当前位置之前的关键帧重新解码，画面会停顿片刻。`VideoTrack()` 是正在播放的清晰度的轨道。

```go
for _, v := range p.Info().Variants() {
	fmt.Println(v.ID, v.Width, v.Height, v.Bandwidth)
}
p.SelectVariant(-1) // 交还给带宽选择
```

## 加密

用 AES-128 加密的 HLS 分片会被解密。DRM 不在范围内。

## 单独的音频

站点把流的音频和画面分开提供时，用 `player.ExternalAudio` 传入音频。它的声音仍是时钟，`Buffered` 取两个媒体源中较少的那个。

```go
p.Open(videoURL, player.Headers(h), player.ExternalAudio(audioURL, h))
```
