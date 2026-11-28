---
title: FFmpeg 库
description: 构建 FFmpeg 的 LGPL 库，了解内核从哪里加载它们、它们怎样进入你的应用，以及分发它们要承担的义务。
---

内核通过 purego 调用 FFmpeg，不用 cgo，并在运行时加载它的共享库。它只认 FFmpeg 9 的主版本，遇到其他版本直接拒绝，而不去猜测结构布局。

## 构建

`tools/ffmpeg` 在存储库检出目录里，按固定版本为每个目标构建这些库。它只需要 macOS 上的 Xcode 和 Go，每个下载都用 SHA-256 固定。

```sh
go run ./tools/ffmpeg                    # 构建所有目标到 dist/ffmpeg
go run ./tools/ffmpeg -target linux_x64  # 只构建一个目标
```

| 目标 | 硬件解码 | 运行时需要 |
| --- | --- | --- |
| `macos_arm64`、`macos_x64` | VideoToolbox | macOS 11 |
| `ios`、`ios_simulator`（arm64） | VideoToolbox | iOS 15 |
| `windows_x64` | D3D11VA、DXVA2 | Windows 10 |
| `linux_x64` | VAAPI | glibc 2.34、`libva.so.2` 和 `libva-drm.so.2` |

只配置 LGPL 2.1 的部分：所有解复用器、解析器和解码器，文件协议，变速用到的音频滤镜，直通用的 spdif 复用器，MOV、MP4 和 WebM 复用器，以及 `codec` 和 `mediafile` 用到的编码器：各平台的 H.264 和 HEVC 硬件编码器（Linux 上还有 OpenH264）、ProRes、AAC，以及通过 libvpx 和 libopus（BSD）链接进 libavcodec 的 VP9 和 Opus。没有 x264 这类 GPL 编码器、其他复用器、设备和网络协议；HTTP(S) 在 Go 中读取，所以不链接 TLS 库。libxml2（MIT）链接进 libavformat，用于 DASH。

每个归档 `ffmpeg-<version>-<target>.zip` 包含六个库（iOS 是六个框架），以及许可证要求随库分发的声明：`COPYING.LGPLv2.1`、`COPYING.libxml2`、`COPYING.libvpx`、`COPYING.opus`、Windows 和 Linux 编码器各自的许可证、`ffmpeg.patch` 和 `BUILD.txt`。`dist/ffmpeg/source` 里是 FFmpeg 的发布源码包和补丁，即这些库的完整源码。

## 加载位置

内核按顺序取第一个找到的：

1. 环境变量 `NSGO_MEDIA_FFMPEG` 指定的目录。
2. macOS 上应用包的 `Contents/Frameworks`。
3. 可执行文件所在目录。
4. 系统的 FFmpeg 9：macOS 上是 Homebrew 的前缀，其他平台由加载器查找。

在 iOS 上只从框架加载：`NSGO_MEDIA_FFMPEG` 里的（模拟器测试用），或应用的 `Frameworks` 里的。

## 打包应用

这些库和 iOS 框架作为存储库的 Release [`ffmpeg-9.0.2-3`](https://github.com/nsgo-dev/nsgo-media/releases/tag/ffmpeg-9.0.2-3) 发布，内核向 NSGo 的工具声明了它们。`nsgo build` 会下载它们、校验 SHA-256，放进 macOS 应用的 `Contents/Frameworks`（`macos_universal` 会合并两种架构）、Windows 或 Linux 可执行文件旁边，或 iOS 应用的 `Frameworks`。它们和应用一起签名，所以磁盘映像、安装包、IPA 和公证都包含它们；它们的声明会写进应用的 `THIRD_PARTY_NOTICES.txt`，`core.ThirdPartyNotices()` 返回的就是它。之后不需要别的步骤。

- `nsgo dev`、`nsgo build -run` 以及带 `-target` 的 `nsgo test` 也会放入它们，iOS 和桌面一样。要用自己构建的库运行，用 `NSGO_MEDIA_FFMPEG` 指定它们的目录（比如解压后的 `dist/ffmpeg` 归档），它最优先。
- 应用的 Debian 和 Arch 包会依赖内核从系统加载的包：`linux_x64` 上是 libva 和 PulseAudio 的客户端库，`linux_arm64` 上不打包任何库，还会依赖系统的 FFmpeg 9。你的 `nsgo.yaml` 不需要写这些。
- NSGo 的许可证策略会把它们的 LGPL 当作 copyleft 给出警告。如果你的应用设了 `licenses.copyleft: error`，把 `github.com/nsgo-dev/nsgo-media` 加进 `licenses.allow`。

## 分发它们的应用要承担的义务

这些库采用 LGPL 2.1，所以无论是否开源，应用都只能以可替换的共享库形式、连同它们的声明分发它们（`nsgo build` 就是这样放的），此外还要：

- 提供它们的源码：把 `dist/ffmpeg/source` 里的文件（Release 里也有：`ffmpeg-9.0.2.tar.xz` 和 `ffmpeg.patch`）放在应用下载旁、你控制的服务器上，在分发该构建期间一直提供，或随应用一起分发。只链接到本存储库的 Release 并不够。
- 在关于窗口或文档中说明使用了 LGPL 2.1 下的 FFmpeg 项目的库，以及源码在哪里；关于窗口可以显示 `core.ThirdPartyNotices()`。
- 在你的许可条款中允许修改这些库以及调试所需的逆向工程，并且不主张任何 FFmpeg 商标。
- 保留链接进这些库的库的 BSD 和 MIT 声明，`nsgo build` 会随库一起分发它们。
- 自行处理编解码器专利，H.264、HEVC 和 AAC 编码器和解码器一样；LGPL 不授予任何专利。
- 在 iOS 上，以 `nsgo build` 放入的独立动态框架形式分发，绝不链接进可执行文件。App Store 应用的签名和条款是否允许用户替换它们，由应用自己判断；LGPL 不回答这个问题。
