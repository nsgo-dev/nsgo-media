# NSGo Media

A video player core for NSGo apps: FFmpeg decoding and the picture drawn in a view, with every control left to the app. Read [ARCHITECTURE.md](ARCHITECTURE.md) before coding and [docs/requirements.md](docs/requirements.md) before changing scope.

## Principles

- Treat [NSGo](https://github.com/nsgo-dev/nsgo) as the platform: follow its `AGENTS.md` principles for Go code, purego and documents. Never fork it; propose what the core needs from it as an NSGo issue.
- Treat [shuax/media](https://github.com/shuax/media) and [shuax/player](https://github.com/shuax/player) as design references only. They have no license, so never copy their code.
- Never add a control, string or theme to the core. An app draws those; the demo app shows how.
- Keep NSGo Player's pages, strings and rules free of any one source; a source's logic lives in its plugin, and no plugin or mention of a particular site belongs in this repository. Change [Plugins](docs/plugins.md) before widening what a plugin reaches, and confirm a new host function with the user first.
- Mark unbuilt designs as goals or open questions; never document them as existing behavior.

## Licenses

When adding, changing or patching anything the FFmpeg libraries are built from or linked with, or changing how they are packaged, keep every license obligation in the same change, as [FFmpeg libraries](docs/ffmpeg.md) lists:

- Configure no GPL, nonfree or version 3 component, and link FFmpeg only as replaceable shared libraries.
- Put each linked library's license file into the archive and the libraries' entries of `nsgo-natives.yaml`, so `nsgo build` and `bundle` ship it with the app.
- Add every change to FFmpeg's source to `ffmpegPatches`, with its date and reason, so `ffmpeg.patch`, `BUILD.txt` and `dist/ffmpeg/source` hold it.
- Update [what an app that ships them owes](docs/ffmpeg.md#what-an-app-that-ships-them-owes) when that changes.

## Working with the user

- Confirm before choosing an answer to an open question in [ARCHITECTURE.md](ARCHITECTURE.md#open-questions).
- Commit or push only when the user asks.

## Documentation

- Keep `README.md` and `README.zh-CN.md` equivalent, and `docs/public/en/` and `docs/public/zh/`, the website's docs; everything else, including code comments, is English.
- Update a document only when a fact its reader needs changes, and keep it within its budget.
