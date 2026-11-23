// Command nsgo-player is a plugin-based desktop video player.
package main

import (
	"github.com/nsgo-dev/nsgo"

	"github.com/oneo-me/nsgo-media/app/internal/app"
)

func main() { nsgo.Run(app.Root, nsgo.WindowSize(1100, 700)) }
