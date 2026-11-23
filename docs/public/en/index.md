---
title: NSGo Player
description: A desktop video player that plays your video folders, and every other source through plugins.
---

NSGo Player is the project's desktop video player for macOS, Windows and Linux. It plays the videos in your folders, and every other source comes from a plugin you add. The app itself knows no site: its pages draw what plugins answer.

:::note[In development]
NSGo Player runs from a checkout of the repository. It has no published release yet.
:::

## Run it

From a checkout, with Go and NSGo's tools:

```sh
cd app
go tool nsgo dev
```

Playback needs the FFmpeg libraries; [FFmpeg libraries](core/ffmpeg/#where-they-load-from) says where they load from.

## Playing a video

The sidebar's **Open** page opens any single file or URL. The player page has its own controls: play and pause, a progress bar with the buffered range, volume and mute, speed, the audio and subtitle tracks, a video's quality and parts where its plugin gives them, and fullscreen. A video you left resumes where you stopped, and the next part follows at the end.

| Key | Action |
| --- | --- |
| Space | Play or pause |
| ← and → | Seek 5 seconds back or forward |
| ↑ and ↓ | Volume up or down |
| F | Enter or leave fullscreen |
| Esc | Leave fullscreen |

## Local files

The built-in **Local Files** plugin lists your video folders on its home page, opens their subfolders, searches their names and plays a path you paste. Subtitle files named after a video, such as `Movie.srt` or `Movie.zh-CN.ass`, play with it.

Its folders are a setting: open **Settings**, and under **Local Files** type them in **Video folders**, separated by `;`. Without one, it lists the system's Movies or Videos folder.

## Plugins

Each enabled plugin gets its own section in the sidebar, with its home, its pages and its search. In **Settings → Plugins** you can:

- install a plugin from a folder or a zip, which copies it into the app;
- use a plugin from a path, typing its folder's path in **Use from Path**: the app reads it where it is at every start, which suits a plugin you are writing;
- turn each plugin on or off, edit its settings, and remove it with its data. Removing a plugin used from a path keeps its folder.

Before a plugin is added, the app shows the hosts it may connect to and asks you to approve them. A plugin reaches only those hosts, never your files. See [Writing plugins](plugins/) to make one.

## Language

The app speaks English and Simplified Chinese, and follows the system unless you pick one under **Settings → Language**. Plugins' own texts follow it too.
