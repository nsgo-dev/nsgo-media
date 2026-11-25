---
title: Writing plugins
description: How to add a source of videos to NSGo Player with one JavaScript or TypeScript file and a YAML manifest.
---

A plugin turns one source of videos into pages and streams that [NSGo Player](../) shows and plays. The app calls your module's exports and draws every page itself, so a plugin carries no UI and works the same on every desktop. [`app/examples/demo-plugin`](https://github.com/nsgo-dev/nsgo-media/tree/main/app/examples/demo-plugin) uses every export and host function.

## A plugin's folder

```text
my-plugin/
├── plugin.yaml        the manifest
├── index.ts           one module, without imports
└── locales/
    └── zh-CN.yaml     translations of the plugin's own texts
```

A plugin is one JavaScript or TypeScript module. One that uses libraries bundles them into it.

## The manifest

`plugin.yaml` declares everything you must approve or fill in before any of the plugin's code runs:

```yaml
id: demo
name: Demo
version: 1.0.0
minAppVersion: 0.1.0
main: index.ts
media: video
icon: icon.svg
color: "#e8553a"
permissions:
  network: [demo.example.com, "*.cdn.example.com"]
settings:
  - key: quality
    label: Default quality
    type: choice          # text, secret, toggle or choice
    default: "720"
    choices:
      - { value: "1080", label: 1080p }
      - { value: "720", label: 720p }
navigation:
  - { id: favorites, label: Favorites }
statuses:
  - { id: watching, label: Watching }
  - { id: completed, label: Completed }
```

- `permissions.network` lists the only hosts `fetch` reaches, and the only hosts the app fetches your pictures, streams and subtitles from. `*.example.com` matches its subdomains.
- `settings` appear in the app's settings; your code reads them with `host.settings.get`.
- `icon` is an SVG file of the folder, drawn in white on `color` to mark your videos, uploaders and shows among other plugins'; either left out is chosen by the `id`.
- `navigation` lists your own pages, each opening `browse` with its `id`: **Discover** shows the first, and the others while your videos show alone.
- `statuses` are those a followed show can have; a plugin exporting `follow` lists at least one.
- An unknown field is refused. A `minAppVersion` newer than the app is refused with a message to update it.

## Translations

`locales/<language>.yaml` maps each text as your plugin writes it to its translation. It covers the name, labels and anything passed to `host.t`:

```yaml
Demo: 演示
Favorites: 收藏
Default quality: 默认清晰度
```

## Exports

Export the functions you support; the app leaves out what you do not export. A plugin without `search` is left out of the search.

| Export | Answers |
| --- | --- |
| `home`, `browse` | A page of sections of videos, uploaders, collections and links; `browse` opens one by id |
| `search`, `suggest`, `resolve` | Search results by kind, completions, and the item a pasted link names |
| `video` | A video's details, its parts, the part to start with and related videos |
| `stream` | The video and audio URLs of a part at a quality, their request headers, the qualities and the subtitle tracks |
| `progress` | Nothing; it tells you the position the viewer reached |
| `account`, `signIn`, `signOut` | The signed-in account, or `null` |
| `following` | The shows the account follows, newest update first: each with the video that opens it, its status, the episodes `watched` and out, and whether one is `new` |
| `follow`, `unfollow` | Nothing; they follow a show with a status, change its status, or unfollow it on the account |
| `danmaku` | The comments of a span of a part, at most 20,000: each with its `time` in seconds, its `mode` (`scroll`, `top` or `bottom`), an optional `#rrggbb` `color` and its `text` |
| `setSetting` | Nothing; it tells you a setting changed |

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

Following lives on the account, not in the app. A show's episodes are a video's parts, and `video` answers its `show`, with its `status` (`""` when not followed) and episodes `watched`, so its page can follow it and play the next episode:

```ts
export function video({ id }) {
  return { id, title: "A show", parts: [/* its episodes */], show: { id: "s1", status: "watching", watched: 2 } };
}
```

A subtitle track is a URL of SRT, WebVTT or ASS, or its `text`; convert a site's own format in the plugin. Throw an error with a `code` to tell the app what to do: `signIn` asks the viewer to sign in, `setup` opens your settings and `unavailable` marks the video unplayable. Any other error shows its message.

## The host object

Besides the JavaScript standard library, timers, `fetch`, `URL` and `console`, a plugin has `host`:

| Member | Purpose |
| --- | --- |
| `language`, `t(text)` | The viewer's language and your translation of a text |
| `storage.get`, `set`, `remove` | JSON values that outlive the app, in the plugin's own data folder |
| `settings.get(key)` | The viewer's value of a setting |
| `md5(text)` | An MD5 hash, for sites that sign requests with one |
| `showSignIn({ qrCode, message })`, `closeSignIn()` | A sign-in prompt with a QR code; its promise tells whether the viewer closed it |
| `openURL(url)` | Opens a page in the browser |

## The sandbox

Each plugin runs in its own [moejs](https://github.com/MoeForge/moejs) runtime with a memory limit, and each call has a deadline. A plugin has no file system, no processes and no `eval`. A runtime that runs too long without waiting, or uses too much memory, is stopped, and the next call starts a fresh one.

## Trying it

While you write a plugin, choose **Settings → Plugins → Add Plugin → Use from Path…** in NSGo Player and pick its folder. The app reads it where it is at every start, so restart the app to load your changes. To share a plugin, zip its folder; others install it from the zip.

## Publishing on GitHub

To let others install your plugin by typing your repository, publish a GitHub release of it:

1. Raise `version` in `plugin.yaml`, and `minAppVersion` if it needs a newer app.
2. Zip the plugin's folder, with `plugin.yaml` at the zip's root or in its only folder, and attach the zip to a release that is not a pre-release. Other attached files are fine; the app takes the first `.zip` holding a plugin, of at most 64 MB. GitHub's own source code archives do not count.
3. Write what changed in the release's description: the app shows it when it offers the update.

The app offers a release as an update when it is the repository's latest and its `version` is newer than the one installed, with the same `id`. Pre-releases and drafts are never installed. An update that adds hosts asks the viewer to approve them first.
