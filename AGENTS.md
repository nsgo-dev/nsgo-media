# NSGo Media

A video player core for NSGo apps: FFmpeg decoding and the picture drawn in a view, with every control left to the app. Read [ARCHITECTURE.md](ARCHITECTURE.md) before coding and [docs/requirements.md](docs/requirements.md) before changing scope.

## Principles

- Treat [NSGo](https://github.com/nsgo-dev/nsgo) as the platform: follow its `AGENTS.md` principles for Go code, purego and documents. Never fork it; propose what the core needs from it as an NSGo issue.
- Treat [shuax/media](https://github.com/shuax/media) and [shuax/player](https://github.com/shuax/player) as design references only. They have no license, so never copy their code.
- Never add a control, string or theme to the core. An app draws those; the demo app shows how.
- Mark unbuilt designs as goals or open questions; never document them as existing behavior.

## Working with the user

- Confirm before choosing an answer to an open question in [ARCHITECTURE.md](ARCHITECTURE.md#open-questions).
- Commit or push only when the user asks.

## Documentation

- Keep `README.md` and `README.zh-CN.md` equivalent; everything else, including code comments, is English.
- Update a document only when a fact its reader needs changes, and keep it within its budget.
