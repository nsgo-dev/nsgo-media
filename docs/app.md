# NSGo Player

NSGo Player, in `app/`, is NSGo Media's first-party desktop app and its fullest demo: a video player whose pages draw what [plugins](plugins.md) answer, and whose player page drives a `player.Player` with its own controls. It is a Go module of its own, so the core's module never depends on what only the app uses, such as moejs; it reaches the core through a `replace` of the repository's root, and the core's public API alone.

## Scope

It runs from a checkout, and installs from the packages `nsgo build` writes, on macOS (arm64 and x64), Windows x64 and Linux x64; no release is published.

| Area | Built |
| --- | --- |
| Local playback | Opening a local file or a URL, and every format the core decodes |
| Player | A borderless 960×540 window of its own, moved by dragging the picture and kept above other windows when pinned (P), at 560×315 with fewer controls; a slim bar of play, pause, a seek line with the buffered range, volume, mute, the time played and its length or what is left, rate, subtitle and audio track choice, a video's parts, quality and full screen; keyboard shortcuts; the bars and the pointer fading out as the pointer leaves the window or rests for 1.5 s; and resuming where a video was left |
| Danmaku | The comments a plugin's `danmaku` answers over the picture: scrolling across lanes, fixed at the top and the bottom, in their colors with a shadow, smaller in a pinned window; toggled with D, and set by kind, opacity, area, size and speed, kept in the settings |
| Local files | The built-in plugin: the viewer's video folders, their subfolders, searching their names, a pasted path, and subtitle files beside a video |
| Plugins | A folder with a YAML manifest, YAML locales and one JavaScript or TypeScript file, run by moejs with only the network hosts its manifest lists |
| Pages | One toolbar of Home, Following, Discover and Open, search across every plugin (⌘K or Ctrl K) and the settings, whose pages merge the enabled plugins' answers: sections of one title as one, every card marked with its plugin's icon, and a filter showing one plugin alone. Home continues the videos left partway, Discover shows each plugin's first page of its own and, for one plugin, its others; video pages with their parts; playing what the plugin's `stream` answers, at a quality the viewer picks |
| Following | The shows the accounts of the plugins with `following` follow, merged, by status, its tab counting those with new episodes; a show's video page following it, changing its status or unfollowing it, and playing the episode after the last watched; all kept on the accounts, not in the app |
| Plugin management | Listing, enabling and disabling plugins, installing one from the latest release of a GitHub repository, a folder or a zip, or using one where its folder is picked, each after approving its hosts; checking the repositories of those from GitHub for a newer release and updating one when the viewer agrees, keeping its data; each plugin's page with its account, settings and hosts, and removing it with its data; listing those refused until removed |
| Look | Every page and state of the designs in `design/` |
| Languages | Simplified Chinese and English, for the app and for plugins' own text |
| Systems | The same pages, keys and plugins on every desktop, the player's controls Liquid Glass where macOS draws it and a tinted blur elsewhere, the local files plugin listing the folder the system names for videos: a disk image on macOS, an NSIS installer on Windows, and a Debian package, an Arch package and an AppImage on Linux, each with the FFmpeg libraries; the Linux packages depend on libva and PulseAudio's client libraries, which the libraries and the core load |

Out of scope: comments under a video and posting to a site; downloading, casting, live streams and DRM; a catalog of plugins; the app updating itself; publishing releases; playlists; mobile targets. No plugin for a particular site ships in this repository.

## Code map

| Directory | Responsibility |
| --- | --- |
| `app/main.go` | The app's entry: its window and root |
| `app/internal/app` | Pages and components: the toolbar, the merged pages and their source filter, search, the video page, settings, the player window, and the plugins the library loads |
| `app/internal/playback` | The player page and its controls around a player, the quality and part choice, pinning, danmaku, and resume positions |
| `app/internal/plugin` | Loading manifests, the moejs runtimes, native plugins, the `host` object, permissions, and the Go types of the contract |
| `app/internal/store` | The app's settings, installed plugins, their data folders, resume positions and the videos played last on disk |
| `app/locales` | The app's words in English and Simplified Chinese |
| `app/plugins/local` | The built-in local files plugin, in Go |
| `app/examples/demo-plugin` | A TypeScript plugin of made-up videos using every export and host function, which the tests load |

## Boundaries

- Pages never call moejs or a native plugin. They call `internal/plugin`'s `Plugin`, which validates every answer against the contract before a page sees it.
- Plugin calls run off NSGo's engine goroutine and reach the reactive graph only through `Scope.Post`. A page shows loading and errors for each call.
- No page, string or rule is specific to one source. What one plugin needs, any plugin can use; a source shows only by the name and icon its manifest gives.
- The player knows sources, headers and tracks, never plugins: `internal/playback` takes what `stream` answered, with `file:` URLs turned into paths, and nothing else.

## Flows

**Start.** The app reads its settings, loads the built-in plugins and the installed ones from its plugins folder, and refuses any whose manifest is invalid or needs a newer app. Every enabled plugin's answers join the toolbar's pages, and a module plugin gets a runtime on first use.

**Browse.** A toolbar's page calls the export of every enabled plugin with it at once, or of the one the filter chose, and renders the sections they answer as they come: cards of videos, uploaders and collections, and links to further pages; a plugin that fails leaves a banner of what to do over the others' cards. A search first asks every plugin with `resolve` whether it names a link or ID, then searches; completions are theirs together. Opening a video calls `video` for its parts and related videos, and the show it belongs to, which follow, a status of the manifest's, and unfollow change, showing the change at once and undoing it if the call fails. The Following tab and page ask `following` again after signing in or out and after a follow changes.

**GitHub.** A repository typed as `owner/repo` or its URL is asked for its latest release, whose plugin zip is downloaded and read as a picked zip is, then approved and installed ([Publishing on GitHub](plugins.md#publishing-on-github)). At start, every six hours and on Refresh, the app asks for each such plugin's latest release and reads a newer one ahead, so its badge, notes and added hosts show before the viewer agrees. An update closes the plugin, swaps its folder, loads the new one and puts it in the old one's place in the pages; one that fails to load puts the old folder back.

**Play.** Playing a part calls `stream` and opens its video, and its audio if separate, with their headers, in the player window, which a second source replaces; where the platform opens no windows, the page covers the main window instead. `danmaku` is asked for six minutes of the part at a time, ahead of the position. The quality menu calls `stream` again and resumes at the same position. The position is stored locally and reported to the plugin's `progress`; at the end, the next part follows.

## Data

The app keeps its data in the user's configuration directory: its settings, which list the folders of the plugins used where they are, the repository and release each plugin from GitHub came from, whether it sends usage statistics and how danmaku shows, the installed plugins' folders, each plugin's data folder with its storage and settings, the resume positions, and the 50 videos of plugins played last, as their plugins answered them, for Home to continue. Only the app writes them; a plugin writes only its own storage, through the host object.

The installed app counts its use in the website's Umami (`internal/stats`): a pageview for each kind of page, never a plugin's own, heartbeats while a page is new or a video is open, and a random ID kept in its storage, until the viewer turns it off in the settings.

## Verification

From `app/`, `go tool nsgo check` and `go tool nsgo test` cover its packages. Plugin tests load the demo plugin and check permissions, limits, errors and every host function, and native plugins' file URLs. The app's tests follow, change and unfollow a demo show, a failed follow going back, and the following page's loading, empty, sign-in and error states. A local server stands in for GitHub's API and downloads in the tests of installing and updating from GitHub: a repository missing, without a release or a plugin zip, a pre-release, a limited or unreachable API, a newer `minAppVersion`, added hosts, data kept and a failed update rolled back. The local files plugin's tests browse, search and stream a folder made for them, and the app's play a clip of the core's `testdata/` from it. The app's tests merge two plugins' pages and filter them. The player page's tests play the core's clips, headless and in a real window with `-target`, pin a window of its own, hide the bars as the pointer rests and leaves, and lay out and hide danmaku. On Linux (Omarchy, Hyprland on Wayland, PipeWire) and Windows 11 (2026-11-24) the app's tests passed in real windows, a plugin used from its folder played a DASH stream with separate audio and headers in one, and the AppImage and the NSIS installer `nsgo build` wrote, the latter installing for the user under `%LOCALAPPDATA%\Programs`, played a local file with their own FFmpeg libraries, not the system's. Headless on Windows, which has no FFmpeg of its own, `NSGO_MEDIA_FFMPEG` names the libraries. Installing a plugin from a real GitHub release is untried on either.
