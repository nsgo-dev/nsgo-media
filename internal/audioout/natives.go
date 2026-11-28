package audioout

import "embed"

// nsgo-natives.yaml names the Linux packages of PulseAudio's client
// library for NSGo's tool. Embedding it keeps it in the package's directory
// when an app vendors it.
//
//go:embed nsgo-natives.yaml
var _ embed.FS
