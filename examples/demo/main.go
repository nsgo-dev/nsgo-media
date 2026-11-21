// Command demo is a player app with its own controls, showing how an app
// drives NSGo Media's core.
package main

import (
	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
)

// Root is the main window's content.
func Root(cx *core.Scope) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, Background: core.RGB(0x000000)}}
}

func main() { nsgo.Run(Root, nsgo.WindowSize(960, 600)) }
