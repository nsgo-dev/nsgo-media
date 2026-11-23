---
title: 编写插件
description: 用一个 JavaScript 或 TypeScript 文件和一份 YAML 清单，给 NSGo Player 添加视频来源。
---

插件把一个视频来源变成 [NSGo Player](../app/) 显示的页面和播放的流。应用调用你模块的导出函数，所有页面都由应用自己画，所以插件不带界面，在每个桌面平台上都一样。[`app/examples/demo-plugin`](https://github.com/nsgo-dev/nsgo-media/tree/main/app/examples/demo-plugin) 用到了每个导出函数和宿主函数。

## 插件的文件夹

```text
my-plugin/
├── plugin.yaml        清单
├── index.ts           一个模块，不能有 import
└── locales/
    └── zh-CN.yaml     插件自己文本的翻译
```

插件只有一个 JavaScript 或 TypeScript 模块，用到的库要打包进去。

## 清单

`plugin.yaml` 声明在运行插件任何代码之前，需要你确认或填写的一切：

```yaml
id: demo
name: Demo
version: 1.0.0
minAppVersion: 0.1.0
main: index.ts
media: video
permissions:
  network: [demo.example.com, "*.cdn.example.com"]
settings:
  - key: quality
    label: Default quality
    type: choice          # text、secret、toggle 或 choice
    default: "720"
    choices:
      - { value: "1080", label: 1080p }
      - { value: "720", label: 720p }
navigation:
  - { id: favorites, label: Favorites }
```

- `permissions.network` 列出 `fetch` 能访问的全部主机，应用也只从这些主机获取你给出的图片、流和字幕。`*.example.com` 匹配它的子域名。
- `settings` 显示在应用的设置里，代码用 `host.settings.get` 读取。
- `navigation` 添加侧栏入口，每个入口用它的 `id` 打开 `browse`。
- 不认识的字段会被拒绝。`minAppVersion` 比应用新时也会拒绝，并提示更新应用。

## 翻译

`locales/<语言>.yaml` 把插件里写的每段文本映射到译文，包括名字、各种标签和传给 `host.t` 的文本：

```yaml
Demo: 演示
Favorites: 收藏
Default quality: 默认清晰度
```

## 导出函数

导出你支持的函数，没导出的应用就不显示。没有 `search` 的插件就没有搜索框。

| 导出 | 返回 |
| --- | --- |
| `home`、`browse` | 一页分区，包含视频、作者、合集和链接；`browse` 按 id 打开 |
| `search`、`suggest`、`resolve` | 按类型的搜索结果、补全，以及粘贴的链接对应的条目 |
| `video` | 视频的详情、分 P、起始分 P 和相关视频 |
| `stream` | 某个分 P 在某个清晰度下的视频和音频地址、请求头、可选清晰度和字幕轨 |
| `progress` | 不返回；告诉你观看到的位置 |
| `account`、`signIn`、`signOut` | 已登录的账号，或 `null` |
| `setSetting` | 不返回；告诉你某项设置变了 |

```ts
export async function stream({ video, part, quality }) {
  return {
    video: { url: "https://cdn.example.com/v.m4s", headers: { Referer: "https://example.com/" } },
    audio: { url: "https://cdn.example.com/a.m4s", headers: { Referer: "https://example.com/" } },
    quality: "720",
    qualities: [{ id: "1080", label: "1080p" }, { id: "720", label: "720p" }],
    subtitles: [{ label: "English", url: "https://cdn.example.com/en.vtt" }],
  };
}
```

字幕轨是 SRT、WebVTT 或 ASS 的地址，或者直接给 `text`；网站自己的格式请在插件里转换。抛出带 `code` 的错误告诉应用该做什么：`signIn` 请用户登录，`setup` 打开插件设置，`unavailable` 把视频标为无法播放。其他错误直接显示它的消息。

## 宿主对象

除了 JavaScript 标准库、定时器、`fetch`、`URL` 和 `console`，插件还有 `host`：

| 成员 | 用途 |
| --- | --- |
| `language`、`t(text)` | 用户的语言，以及文本的译文 |
| `storage.get`、`set`、`remove` | 在应用重启后仍保留的 JSON 值，存在插件自己的数据文件夹里 |
| `settings.get(key)` | 用户设置的值 |
| `md5(text)` | MD5 哈希，给用它签名请求的网站 |
| `showSignIn({ qrCode, message })`、`closeSignIn()` | 带二维码的登录提示；它的 Promise 表示用户是否关闭了提示 |
| `openURL(url)` | 在浏览器里打开网页 |

## 沙箱

每个插件运行在自己的 [moejs](https://github.com/MoeForge/moejs) 运行时里，有内存上限，每次调用都有时限。插件没有文件系统、不能启动进程，也没有 `eval`。运行太久不等待、或者内存超限的运行时会被停止，下一次调用会重新启动一个。

## 试用

编写插件时，在 NSGo Player 里打开 **设置 → 插件 → 从路径使用**，填入插件文件夹的路径。应用每次启动都从原处读取，所以改完重启应用就能加载。要分享插件，把文件夹打成 zip，别人就能从 zip 安装。
