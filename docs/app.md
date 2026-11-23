# NSGo Player

NSGo Player, in `app/`, is NSGo Media's first-party desktop app and its fullest demo: a video player whose pages draw what [plugins](plugins.md) answer, and whose player page drives a `player.Player` with its own controls. It is a Go module of its own, so the core's module never depends on what only the app uses, such as moejs; it reaches the core through a `replace` of the repository's root, and the core's public API alone.

## Scope

It builds and runs from a checkout on macOS (arm64 and x64), Windows x64 and Linux x64. Its first release is on macOS; Windows and Linux follow it.

| Area | Built |
| --- | --- |
| Local playback | Opening a local file or a URL, and every format the core decodes |
| Player | Play, pause, a progress bar with the buffered range, seeking, volume, mute, rate, subtitle and audio track choice, fullscreen, keyboard shortcuts, and resuming where a video was left |
| Local files | The built-in plugin: the viewer's video folders, their subfolders, searching their names, a pasted path, and subtitle files beside a video |
| Plugins | A folder with a YAML manifest, YAML locales and one JavaScript or TypeScript file, run by moejs with only the network hosts its manifest lists |
| Plugin pages | A sidebar entry per plugin with its home, its own pages and search; video pages with their parts; playing what the plugin's `stream` answers, at a quality the viewer picks |
| Plugin management | Listing, enabling and disabling plugins, installing one from a folder or zip, or using one where its folder is from a path typed in the settings, each after approving its hosts; removing it with its data, and editing its settings |
| Languages | Simplified Chinese and English, for the app and for plugins' own text |

### First version on macOS

Goals, in the [roadmap](https://github.com/oneo-me/nsgo-media/issues/2)'s order:

- A designed look: every page drawn from designs made before it is built.
- Installing from GitHub: typing a repository as `owner/repo` or its URL installs the plugin zip of its latest release, after the viewer approves its hosts. The app checks the repositories of plugins installed this way for a newer release, never a pre-release, and updates one when the viewer agrees.
- A release: one universal disk image, signed with a Developer ID, notarized and published on this repository's GitHub releases by a local command. A plugin from its own repository, installed from GitHub, plays in it.

Out of scope: danmaku, comments and posting to a site; downloading, casting, live streams and DRM; a catalog of plugins; the app updating itself; playlists; mobile targets. No plugin for a particular site ships in this repository.

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

**Play.** Playing a part calls `stream` and opens its video, and its audio if separate, with their headers. The quality menu calls `stream` again and resumes at the same position. The position is stored locally and reported to the plugin's `progress`; at the end, the next part follows.

## Data

The app keeps its data in the user's configuration directory: its settings, which list the folders of the plugins used where they are, the installed plugins' folders, each plugin's data folder with its storage and settings, and the resume positions. Only the app writes them; a plugin writes only its own storage, through the host object.

## Verification

From `app/`, `go tool nsgo check` and `go tool nsgo test` cover its packages. Plugin tests load the demo plugin and check permissions, limits, errors and every host function, and native plugins' file URLs. The local files plugin's tests browse, search and stream a folder made for them, and the app's play a clip of the core's `testdata/` from it. The player page's tests play the core's clips, headless and in a real window with `-target`.
