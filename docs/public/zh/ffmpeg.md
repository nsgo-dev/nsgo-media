---
title: FFmpeg 库
description: 构建 FFmpeg 的 LGPL 库，了解内核从哪里加载它们，把它们放进你的应用，以及分发它们要承担的义务。
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

只配置 LGPL 2.1 的部分：所有解复用器、解析器和解码器，文件协议，变速用到的音频滤镜，以及直通用的 spdif 复用器。没有编码器、其他复用器、设备和网络协议；HTTP(S) 在 Go 中读取，所以不链接 TLS 库。libxml2（MIT）链接进 libavformat，用于 DASH。

每个归档 `ffmpeg-<version>-<target>.zip` 包含六个库（iOS 是六个框架），以及许可证要求随库分发的声明：`COPYING.LGPLv2.1`、`COPYING.libxml2`、`ffmpeg.patch` 和 `BUILD.txt`。`dist/ffmpeg/source` 里是 FFmpeg 的发布源码包和补丁，即这些库的完整源码。

## 加载位置

内核按顺序取第一个找到的：

1. 环境变量 `NSGO_MEDIA_FFMPEG` 指定的目录。
2. macOS 上应用包的 `Contents/Frameworks`。
3. 可执行文件所在目录。
4. 系统的 FFmpeg 9：macOS 上是 Homebrew 的前缀，其他平台由加载器查找。

在 iOS 上只从框架加载：`NSGO_MEDIA_FFMPEG` 里的（模拟器测试用），或应用的 `Frameworks` 里的。

## 打包应用

`go tool nsgo-media bundle` 把这些库放进 `nsgo build` 生成的包里：macOS 应用的 `Contents/Frameworks`、iOS 应用的 `Frameworks`、Windows 或 Linux 可执行文件所在的目录，并把归档里的所有声明放进 `FFmpeg/`。

```sh
go get -tool github.com/oneo-me/nsgo-media/cmd/nsgo-media
go tool nsgo build -installers=false
go tool nsgo-media bundle dist/*   # 用 -libs DIR 从别处取归档
```

- 它使用 `tools/ffmpeg` 构建的归档，不下载任何东西。它先找当前目录的 `dist/ffmpeg`，再找本模块目录的 `dist/ffmpeg`；对你的应用来说，就是 `replace` 指令指向的本地检出。
- 它从包名的结尾识别每个包的目标，并为 `macos_universal` 用 `lipo` 合并两种架构的库。
- macOS 应用会用它原有的身份重新签名（`-sign` 可指定别的）。经过 `nsgo build` 公证的应用需要重新公证。
- 已签名的 iOS 应用会重新签名，旁边的 IPA 也会重新生成。`nsgo build -run` 和 `nsgo dev` 会在 `bundle` 运行前就安装应用，所以用 `sign.profile` 构建真机版本时，先 bundle，再用 `xcrun devicectl device install app` 安装。
- `nsgo build` 生成的安装包在 `bundle` 运行前就已做好，不含这些库，所以构建时加上 `-installers=false`，之后再制作安装包。

## 分发它们的应用要承担的义务

这些库采用 LGPL 2.1，所以无论是否开源，应用都只能以可替换的共享库形式分发它们（`bundle` 就是这样放的），此外还要：

- 提供它们的源码：把 `dist/ffmpeg/source` 里的文件放在应用下载旁、你控制的服务器上，在分发该构建期间一直提供，或随应用一起分发。
- 在关于窗口或文档中说明使用了 LGPL 2.1 下的 FFmpeg 项目的库，以及源码在哪里。
- 在你的许可条款中允许修改这些库以及调试所需的逆向工程，并且不主张任何 FFmpeg 商标。
- 自行处理编解码器专利；LGPL 不授予任何专利。
- 在 iOS 上，以 `bundle` 放入的独立动态框架形式分发，绝不链接进可执行文件。App Store 应用的签名和条款是否允许用户替换它们，由应用自己判断；LGPL 不回答这个问题。
