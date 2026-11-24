---
title: NSGo Player
description: A desktop video player that plays your video folders, and every other source through plugins.
---

NSGo Player is the project's desktop video player for macOS, Windows and Linux. It plays the videos in your folders, and every other source comes from a plugin you add. The app itself knows no site: its pages draw what plugins answer.

:::note[In development]
NSGo Player runs from a checkout of the repository. It has no published release yet.
:::

## Run it

From a checkout, with Go, on macOS, Windows or Linux:

```sh
cd app
go tool nsgo dev
```

It downloads the FFmpeg libraries playback needs and puts them beside the app; [FFmpeg libraries](core/ffmpeg/#where-they-load-from) says where they load from. On Linux it needs GTK 4, libva and PulseAudio's client libraries, or PipeWire's PulseAudio server, which desktop distributions install.

## Install it

`go tool nsgo build` in `app/` writes the packages for every desktop into `app/dist/releases`, each holding the FFmpeg libraries:

| System | Package | Install |
| --- | --- | --- |
| macOS | `nsgo-player-…-macos-arm64.dmg` or `…-macos-x64.dmg` | Open it and drag NSGo Player to Applications |
| Windows | `nsgo-player-…-windows-x64-setup.exe` | Run it; building it needs NSIS's `makensis` |
| Debian, Ubuntu | `nsgo-player-…-linux-x64.deb` | `sudo apt install ./nsgo-player-…-linux-x64.deb` |
| Arch | `nsgo-player-…-linux-x64.pkg.tar.gz` | `sudo pacman -U nsgo-player-…-linux-x64.pkg.tar.gz` |
| Any Linux | `nsgo-player-…-linux-x64.AppImage` | Make it executable and run it |

`-target` builds one system's, as `go tool nsgo build -target linux_x64`. The pages and the keys below are the same on every system.

## Playing a video

The sidebar's **Open** page opens a file you pick or a URL you paste, and a file dropped anywhere on the window plays. The player covers the window, its bars over the picture hiding after three seconds of stillness while it plays: play and pause, a progress bar with the buffered range, volume and mute, speed, one menu of the subtitle and audio tracks, a video's parts, next part and quality where its plugin gives them, and fullscreen. A video's page offers to resume the part you left where you stopped, or to play it from the start; the next part follows at the end.

| Key | Action |
| --- | --- |
| Space | Play or pause |
| ← and → | Seek 5 seconds back or forward |
| ↑ and ↓ | Volume up or down |
| F | Enter or leave fullscreen |
| Esc | Close a menu, leave fullscreen, then the player |

## Local files

The built-in **Local Files** plugin lists your video folders on its home page, opens their subfolders, searches their names and plays a path you paste. Subtitle files named after a video, such as `Movie.srt` or `Movie.zh-CN.ass`, play with it.

Its folders are a setting: open **Settings**, press **Local Files** in the list of plugins, and type them in **Video folders**, separated by `;`. Without one, it lists the system's Movies or Videos folder.

## Plugins

Each enabled plugin gets its own section in the sidebar, with its home, its pages and its search, and signing in to its account when it has one. **Settings → Plugins** lists every plugin with its switch, and **Add Plugin** adds one:

- **From GitHub…** installs the plugin zip of a repository's latest release: type the repository as `owner/repo` or paste its URL. If the repository does not exist, has no release, or its latest release has no plugin zip, the dialog says which;
- **From Folder…** or **From Zip…** copies a plugin into the app;
- **Use from Path…** uses the folder you pick where it is: the app reads it there at every start, which suits a plugin you are writing.

Pressing a plugin in the list opens its page: its switch, its account, its settings, the hosts it may connect to, and **Remove Plugin…**, which removes it with its data. Removing a plugin used from a path keeps its folder. A plugin the app cannot load, as one needing a newer app, stays in the list with why until you remove it.

The app checks the repositories of plugins installed from GitHub for a newer release, never a pre-release, when it starts and every six hours; the refresh button beside **Plugins** checks now. A newer one shows as **Update to …** on the plugin's row, on its page and as a count on **Settings**. Updating shows the release's notes, asks you to approve any host it adds, and keeps the plugin's settings, data and sign-in; a release that needs a newer app is refused.

Before a plugin is added, the app shows the hosts it may connect to and asks you to approve them. A plugin reaches only those hosts, never your files. See [Writing plugins](plugins/) to make one.

## Language

The app speaks English and Simplified Chinese, and follows the system unless you pick one under **Settings → Language**. Plugins' own texts follow it too.

## Usage statistics

The app counts its use anonymously: which kind of page shows, never a plugin's or a video's, the window's size, the system and its language, with a random ID for the install. Turn it off under **Settings → Send Usage Statistics**.
