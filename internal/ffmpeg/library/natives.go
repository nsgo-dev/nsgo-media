package library

import "embed"

// nsgo-natives.yaml declares the libraries and iOS frameworks, published as
// a release of this repository, that NSGo's tool puts into an app, where
// Dirs finds them, with the notices under notices/. Embedding them keeps
// them in the package's directory when an app vendors it. go run
// ./tools/ffmpeg -natives writes them.
//
//go:embed nsgo-natives.yaml notices
var _ embed.FS
