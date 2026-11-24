# NSGo Player

NSGo Player, in `app/`, is NSGo Media's first-party desktop app and its fullest demo: a video player whose pages draw what [plugins](plugins.md) answer, and whose player page drives a `player.Player` with its own controls. It is a Go module of its own, so the core's module never depends on what only the app uses, such as moejs; it reaches the core through a `replace` of the repository's root, and the core's public API alone.

## Scope

It runs from a checkout, and installs from the packages `nsgo build` writes, on macOS (arm64 and x64), Windows x64 and Linux x64; no release is published.

| Area | Built |
| --- | --- |
| Local playback | Opening a local file or a URL, and every format the core decodes |
| Player | Play, pause, a progress bar with the buffered range, seeking, volume, mute, rate, subtitle and audio track choice, a video's parts, fullscreen, keyboard shortcuts, bars that hide while it plays, and resuming where a video was left |
| Local files | The built-in plugin: the viewer's video folders, their subfolders, searching their names, a pasted path, and subtitle files beside a video |
| Plugins | A folder with a YAML manifest, YAML locales and one JavaScript or TypeScript file, run by moejs with only the network hosts its manifest lists |
| Plugin pages | A sidebar entry per plugin with its home, its own pages and search; video pages with their parts; playing what the plugin's `stream` answers, at a quality the viewer picks |
| Plugin management | Listing, enabling and disabling plugins, installing one from the latest release of a GitHub repository, a folder or a zip, or using one where its folder is picked, each after approving its hosts; checking the repositories of those from GitHub for a newer release and updating one when the viewer agrees, keeping its data; each plugin's page with its account, settings and hosts, and removing it with its data; listing those refused until removed |
| Look | Every page and state of the designs in `design/` but the following page and following a show, which wait for a plugin export of their own |
| Languages | Simplified Chinese and English, for the app and for plugins' own text |
| Systems | The same pages, keys and plugins on every desktop, the player's controls Liquid Glass where macOS draws it and a tinted blur elsewhere, the local files plugin listing the folder the system names for videos: a disk image on macOS, an NSIS installer on Windows, and a Debian package, an Arch package and an AppImage on Linux, each with the FFmpeg libraries; the Linux packages depend on libva and PulseAudio's client libraries, which the libraries and the core load |

Out of scope: danmaku, comments and posting to a site; downloading, casting, live streams and DRM; a catalog of plugins; the app updating itself; publishing releases; playlists; mobile targets. No plugin for a particular site ships in this repository.

## Code map

| Directory | Responsibility |
| --- | --- |
| `app/main.go` | The app's entry: its window and root |
| `app/internal/app` | Pages and components: the sidebar, plugin pages, search, the video page, settings, and the plugins the library loads |
| `app/internal/playback` | The player page and its controls around a player, the quality and part choice, and resume positions |
| `app/internal/plugin` | Loading manifests, the moejs runtimes, native plugins, the `host` object, permissions, and the Go types of the contract |
| `app/internal/store` | The app's settings, installed plugins, their data folders and resume positions on disk |
| `app/locales` | The app's words in English and Simplified Chinese |
| `app/plugins/local` | The built-in local files plugin, in Go |
| `app/examples/demo-plugin` | A TypeScript plugin of made-up videos using every export and host function, which the tests load |

## Boundaries

- Pages never call moejs or a native plugin. They call `internal/plugin`'s `Plugin`, which validates every answer against the contract before a page sees it.
- Plugin calls run off NSGo's engine goroutine and reach the reactive graph only through `Scope.Post`. A page shows loading and errors for each call.
- No page, string or rule is specific to one source. What one plugin needs, any plugin can use.
- The player knows sources, headers and tracks, never plugins: `internal/playback` takes what `stream` answered, with `file:` URLs turned into paths, and nothing else.

## Flows

**Start.** The app reads its settings, loads the built-in plugins and the installed ones from its plugins folder, and refuses any whose manifest is invalid or needs a newer app. Each enabled plugin gets a sidebar section, and a module plugin a runtime on first use.

**Browse.** A sidebar entry or a search calls the plugin's export and renders the sections it answers: cards of videos, uploaders and collections, and links to further pages. Opening a video calls `video` for its parts and related videos.

**GitHub.** A repository typed as `owner/repo` or its URL is asked for its latest release, whose plugin zip is downloaded and read as a picked zip is, then approved and installed ([Publishing on GitHub](plugins.md#publishing-on-github)). At start, every six hours and on Refresh, the app asks for each such plugin's latest release and reads a newer one ahead, so its badge, notes and added hosts show before the viewer agrees. An update closes the plugin, swaps its folder, loads the new one and puts it in the old one's place in the pages; one that fails to load puts the old folder back.

**Play.** Playing a part calls `stream` and opens its video, and its audio if separate, with their headers. The quality menu calls `stream` again and resumes at the same position. The position is stored locally and reported to the plugin's `progress`; at the end, the next part follows.

## Data

The app keeps its data in the user's configuration directory: its settings, which list the folders of the plugins used where they are, the repository and release each plugin from GitHub came from, and whether it sends usage statistics, the installed plugins' folders, each plugin's data folder with its storage and settings, and the resume positions. Only the app writes them; a plugin writes only its own storage, through the host object.

The installed app counts its use in the website's Umami (`internal/stats`): a pageview for each kind of page, never a plugin's own, heartbeats while a page is new or a video is open, and a random ID kept in its storage, until the viewer turns it off in the settings.

## Verification

From `app/`, `go tool nsgo check` and `go tool nsgo test` cover its packages. Plugin tests load the demo plugin and check permissions, limits, errors and every host function, and native plugins' file URLs. A local server stands in for GitHub's API and downloads in the tests of installing and updating from GitHub: a repository missing, without a release or a plugin zip, a pre-release, a limited or unreachable API, a newer `minAppVersion`, added hosts, data kept and a failed update rolled back. The local files plugin's tests browse, search and stream a folder made for them, and the app's play a clip of the core's `testdata/` from it. The player page's tests play the core's clips, headless and in a real window with `-target`. On Linux (Omarchy, Hyprland on Wayland, PipeWire) and Windows 11 (2026-11-24) the app's tests passed in real windows, a plugin used from its folder played a DASH stream with separate audio and headers in one, and the AppImage and the NSIS installer `nsgo build` wrote, the latter installing for the user under `%LOCALAPPDATA%\Programs`, played a local file with their own FFmpeg libraries, not the system's. Headless on Windows, which has no FFmpeg of its own, `NSGO_MEDIA_FFMPEG` names the libraries. Installing a plugin from a real GitHub release is untried on either.
