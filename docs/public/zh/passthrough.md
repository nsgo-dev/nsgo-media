---
title: 音频直通
description: 把 AC-3、E-AC-3、DTS、DTS-HD 和 TrueHD 不经解码送给功放，并得知实际播放的是什么。
---

直通把轨道的音频不经解码，以 IEC 61937 突发包的形式送给输出设备连接的接收端，比如 HDMI 或 S/PDIF 上的功放，由它来解码。应用不设置时直通是关闭的；iOS 不向任何应用提供直通。

```go
p.SetPassthrough(player.PassthroughAC3 | player.PassthroughEAC3 | player.PassthroughDTS |
	player.PassthroughDTSHD | player.PassthroughTrueHD)
p.SetPassthrough(0) // 关闭
```

| 编码 | 直通内容 |
| --- | --- |
| `PassthroughAC3`、`PassthroughEAC3` | AC-3 和 E-AC-3 |
| `PassthroughDTS` | DTS 的核心；未设置 `PassthroughDTSHD` 或设备不支持高码率时，也包括 DTS-HD 的核心 |
| `PassthroughDTSHD` | 完整的 DTS-HD High Resolution 和 Master Audio，走 HDMI 的高码率载波 |
| `PassthroughTrueHD` | 原样的 TrueHD，包括 Atmos，走 HDMI 的高码率载波 |

高码率载波是 8 声道 192 kHz，44.1 kHz 系列则是 176.4 kHz。

## 各平台

| 平台 | 方式 |
| --- | --- |
| Windows | 独占模式的 WASAPI |
| macOS | 独占（hog）并设为编码格式的 CoreAudio 设备 |
| Linux | 带直通格式的 PulseAudio sink，PipeWire 的服务端同样支持 |

## 何时改播 PCM

其他编码的轨道播放 PCM；设置了直通的轨道在以下情况也会播放 PCM：

- 速度不是 1×；
- 是无法直通的增强型 DTS，比如 DTS-ES、DTS 96/24、DTS Express，或未设置 `PassthroughDTSHD` 的 DTS-HD；
- 设备或系统不接受这个流、拒绝它或启动失败。此时如果设置了 `PassthroughDTS`，DTS-HD 会改为直通它的核心。

`p.AudioOutput()` 告诉你当前播放的是什么：

```go
o := p.AudioOutput()
if o.Passthrough {
	fmt.Println("passing", o.Codec, "at", o.SampleRate, "Hz,", o.Channels, "channels")
} else if o.Fallback != player.FallbackNone {
	fmt.Println("PCM because of", o.Fallback, o.Err) // 速度、增强型 DTS 或设备
}
```

`SampleRate` 和 `Channels` 是设备流的：PCM 的或载波的。轨道自身的值在它的 `Track` 里。

## 直通期间

音量不改变声音，静音会发送接收端能理解的静音。设备已播放的计数在跳转和切换轨道时仍是时钟，暂停时发送暂停突发包，让接收端保持锁定。在其编码的轨道播放时改变直通设置，会从当前位置重新开始播放声音。

:::caution[目前的验证]
在一台 Windows 11 电脑的 S/PDIF 输出上，WASAPI 接受了 AC-3 和 DTS。还没有接收端实际解码过这些突发包，也还没有试过支持高码率的设备。
:::
