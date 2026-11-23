# Plugins

A plugin of [NSGo Player](app.md) turns one source of videos into pages and streams the app shows and plays. It is a folder holding `plugin.yaml`, its locales and one JavaScript or TypeScript module, without imports, so a plugin that uses libraries bundles them. The app calls the module's exports and draws every page itself, so plugins carry no UI and work the same on every desktop. The exact shapes live in `app/internal/plugin` as Go types by their JSON names, and [`app/examples/demo-plugin`](../app/examples/demo-plugin) uses every export and host function; this document records the contract's boundaries and reasons.

## Manifest

`plugin.yaml` names the plugin (`id`, `name`, `version`), the oldest app version it works with (`minAppVersion`), its module (`main`) and the media it serves (`media`: `video` now, `audio` reserved). It also lists its permissions, its settings and its sidebar entries. Anything a viewer must approve or fill in is declared here, so the app can show it before running any code. Unknown fields are refused, except that a newer `minAppVersion` is reported first.

`locales/<language>.yaml` maps the plugin's own texts, its name, labels and messages, to their translation; the app picks the locale that suits the viewer's language best.

## Exports

The app calls an export with one object of parameters and awaits its value or promise. What a plugin does not export, the app leaves out: a plugin without `search` gets no search box.

| Export | Purpose |
| --- | --- |
| `account`, `signIn`, `signOut` | The signed-in account, or `null` |
| `home`, `browse` | A page of sections of videos, uploaders, collections and links; `browse` opens one by id |
| `search`, `suggest`, `resolve` | Search results by kind, completions, and turning a pasted link or ID into a video |
| `video` | A video's details, its parts, the part to start with and related videos |
| `stream` | What to play for one part at a quality |
| `progress` | The position the viewer reached, for sources that keep history |
| `setSetting` | A setting the viewer changed |

`stream` answers a video URL, an optional separate audio URL, the request headers each needs, the qualities on offer and the subtitle tracks. Sites often serve DASH with separate video and audio behind a `Referer`, so both belong in the contract rather than in one plugin's workaround. A subtitle track is a URL of SRT, WebVTT or ASS, or their text: a plugin converts a site's own format, as the app reads none.

`video` may name the part to start with, so a pasted link to a video's later part plays that part.

An error with a `code` tells the app what to do: `signIn` asks the viewer to sign in, `setup` opens the plugin's settings, `unavailable` marks the video unplayable. Any other error shows its message.

## Host

A plugin has the JavaScript standard library, timers, `fetch`, `URL`, `console` and a `host` object with:

- storage of JSON values that outlive the app, and the viewer's settings;
- the viewer's language and the plugin's translation of a text from its `locales/`;
- an MD5 hash, for sites that sign requests with one;
- a sign-in prompt showing a QR code and what to do, and opening a page in the browser.

## Built-in plugins

Built-in plugins are embedded in the binary and can be disabled, not removed. A module plugin built in loads through the same loader as installed ones. The local files plugin is native instead: its exports are Go functions, as no sandbox may reach the viewer's files, and its manifest names no module. Its answers pass the same checks, except that its streams and subtitles may be `file:` URLs, which no module's may. It lists the folders of its setting, or the system's videos folder, with the subtitle files named after a video beside it.

## Boundaries

- Each module runs in its own moejs runtime with a memory limit, and each call has a deadline, so a stuck plugin never stalls the UI. Calls run off NSGo's engine goroutine and post results back.
- A runtime that passes its memory limit, or runs JavaScript too long without waiting for the network or a timer, is stopped and its calls fail; the next call starts a fresh one. Waiting counts only against the call's deadline, so a slow site is not mistaken for a stuck plugin.
- `fetch` reaches only the hosts the manifest's `network` permission lists, never every host; without it, nothing. The app's own requests for a plugin's pictures, streams and subtitles use the headers the plugin answers, and an answer pointing elsewhere is refused.
- Modules get no file system, no processes and no `eval`. A need beyond the host object becomes a new host function, reviewed like any API.
- An installed plugin is copied into the app's plugins folder. One used from a path is read where it is, as when it is being written, at every start; removing it removes only its link and its data, never its folder.
- Plugin data stays in the plugin's own data folder; removing a plugin removes it. Signed-in cookies are stored there in plain text and only sent to the plugin's hosts.
- An installed plugin with a newer `minAppVersion` is refused with a message to update the app, and so is an update needing one.

## Publishing on GitHub

NSGo Player installs a plugin from a repository's latest release, found through GitHub's REST API, which never answers a draft or a pre-release. The plugin is the first `.zip` asset of that release holding `plugin.yaml` at its root or in its only folder, within the size limit of any installed plugin; other assets are ignored, and the repository's source archives are not assets. The app keeps the repository and the release's tag for the plugin's id. It checks the repository at start and every six hours, and offers the latest release when its tag changed and its manifest has the same `id` and a newer `version`, read before any of its code runs. Updating replaces only the plugin's folder: its data folder stays, and a release adding hosts asks the viewer to approve them. A release's description is shown as its notes. Unauthenticated requests share GitHub's limit per address, so a check that is limited or offline says so and leaves the plugins as they are.

## Audio mode

The contract stays media-neutral: pages hold items, and `stream` may answer audio alone. An audio mode later adds `audio` plugins and a player without a picture, without changing existing video plugins.
