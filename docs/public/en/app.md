---
title: NSGo Player
description: The first-party desktop app built on NSGo Media, playing your video folders and every other source through plugins.
---

NSGo Player is the desktop app the project builds on its own core, in the repository's `app/` folder. It shows what an app on NSGo Media looks like in full: its own player controls, subtitles, fullscreen, shortcuts and resume positions, around a `player.View`.

Its pages draw what plugins answer, so the app itself knows no site. The built-in **Local Files** plugin plays the videos in your folders; any other source comes from a plugin you add.

:::note[In development]
NSGo Player runs from a checkout on macOS, Windows and Linux. It has no published release yet.
:::

## Run it

NSGo Player is a Go module of its own, so the core never depends on what only the app uses. From the checkout:

```sh
cd app
go tool nsgo dev
```

Playback needs the FFmpeg libraries, as for any app on the core; see [FFmpeg libraries](../ffmpeg/).

## Local files

The Local Files plugin lists your video folders on its home page, opens their subfolders, searches their names and plays a path you paste. Subtitle files named after a video, such as `Movie.srt` or `Movie.zh-CN.ass`, play with it.

Its folders are a setting: open **Settings**, and under **Local Files** type them in **Video folders**, separated by `;`. Without one, it lists the system's Movies or Videos folder.

The sidebar's **Open** page still opens any single file or URL.

## Plugins

In **Settings → Plugins** you can:

- install a plugin from a folder or a zip, which copies it into the app;
- use a plugin from a path, typing its folder's path in **Use from Path**: the app reads it where it is at every start, which suits a plugin you are writing;
- turn each plugin on or off, edit its settings, and remove it with its data. Removing a plugin used from a path keeps its folder.

Before a plugin is added, the app shows the hosts it may connect to and asks you to approve them. See [Writing plugins](../plugins/) to make one.
