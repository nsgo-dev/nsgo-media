package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/icons/lucide"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// toolbarHeight is the unified toolbar's, which the system's window
// buttons center in.
const toolbarHeight = 52

// tabs are the toolbar's pages, each a page of every enabled plugin.
var tabs = []struct {
	kind   routeKind
	id     string
	icon   func() *core.SVG
	label  func(*core.Scope) string
	export string
}{
	{routeHome, "home", lucide.House, locales.Home, plugin.ExportHome},
	{routeFollowing, "following", lucide.Tv, locales.Following, plugin.ExportFollowing},
	{routeDiscover, "discover", lucide.Compass, locales.Discover, plugin.ExportBrowse},
	{routeOpen, "open", lucide.FolderOpen, locales.OpenPage, ""},
}

// toolbar is the main window's only bar: the system's window buttons,
// the pages of every enabled plugin together, search across them and the
// settings, moving the window as the title bar it replaces does.
func (sh *shell) toolbar(cx *core.Scope) core.Node {
	if desktop.IsAvailable(cx) {
		desktop.SetTitleBar(cx, desktop.TitleBarHidden, toolbarHeight)
	}
	var items []core.Node
	for _, t := range tabs {
		children := []core.Node{
			core.Icon{SVG: t.icon(), Class: "size-3.5 text-muted-foreground group-selected:text-accent"},
			core.Text{ContentFunc: func() string { return t.label(cx) }, Class: "text-[13px] font-medium text-muted-foreground group-selected:text-foreground"},
		}
		if t.kind == routeFollowing {
			children = append(children, core.Show{
				When: func() bool { return sh.fresh() > 0 },
				Then: func(cx *core.Scope) core.Node {
					return core.View{TestID: "following-badge", Class: "rounded-full bg-accent px-1.5", Children: []core.Node{
						core.Text{ContentFunc: func() string { return strconv.Itoa(sh.fresh()) }, Class: "text-[10px] font-semibold text-white"},
					}}
				},
			})
		}
		items = append(items, core.View{
			TestID: "nav-" + t.id, Role: core.RoleTab, LabelFunc: func() string { return t.label(cx) },
			Selected: func() bool { return sh.stack.Get()[0].kind == t.kind },
			OnPress:  func(core.PressEvent) { sh.choose(route{kind: t.kind}) },
			Desktop:  core.DesktopViewProps{WindowDrag: core.WindowDragOff},
			Class:    "group h-7 flex-row items-center gap-1.5 rounded-md px-2.5 hover:bg-background/60 selected:bg-background selected:shadow-sm",
			Children: children,
		})
	}
	search := core.View{
		TestID: "nav-search", Role: core.RoleButton, LabelFunc: func() string { return locales.SearchAll(cx) },
		Selected: func() bool { return sh.stack.Get()[0].kind == routeSearch },
		OnPress:  func(core.PressEvent) { sh.startSearch() },
		Desktop:  core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		Class:    "group h-8 w-[240px] flex-row items-center gap-2 rounded-lg border border-border bg-background px-2.5 selected:border-accent/60",
		Children: []core.Node{
			core.Icon{SVG: lucide.Search(), Class: "size-3.5 text-faint-ink group-selected:text-accent"},
			core.Text{ContentFunc: func() string { return locales.SearchAll(cx) }, MaxLines: 1, Class: "flex-1 text-xs text-faint-ink"},
			core.Text{Content: shortcutLabel(cx), Class: "rounded bg-chip px-1.5 py-0.5 text-[10px] font-medium text-faint-ink"},
		},
	}
	cx.OnKey("mod+k", sh.startSearch)
	settings := core.View{
		TestID: "nav-settings", Role: core.RoleTab, LabelFunc: func() string { return locales.Settings(cx) },
		Selected: func() bool { return sh.stack.Get()[0].kind == routeSettings },
		OnPress:  func(core.PressEvent) { sh.choose(route{kind: routeSettings}) },
		Desktop:  core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		Class:    "group size-8 items-center justify-center rounded-lg hover:bg-well selected:bg-well",
		Children: []core.Node{
			core.Icon{SVG: lucide.Settings(), Class: "size-4 text-muted-foreground group-selected:text-foreground"},
			core.Show{
				When: func() bool { return len(sh.updates.Get()) > 0 },
				Then: func(*core.Scope) core.Node {
					return core.View{TestID: "updates-badge", Class: "absolute top-1.5 right-1.5 size-2 rounded-full bg-accent"}
				},
			},
		},
	}
	return core.View{
		TestID:  "toolbar",
		Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOn},
		Class:   "h-[52px] flex-row items-center gap-3 border-b border-border bg-chip/90 px-4",
		StyleFunc: func() core.Style {
			if !desktop.IsAvailable(cx) {
				return core.Style{}
			}
			bar := desktop.TitleBar(cx)
			return core.Style{Padding: core.Edges{Left: max(bar.Left+4, 16), Right: max(bar.Right+4, 16)}}
		},
		Children: []core.Node{
			core.View{TestID: "tabs", Role: core.RoleTabList, Class: "flex-row items-center gap-1 rounded-lg bg-well/60 p-0.5", Children: items},
			core.View{Class: "flex-1"},
			search,
			settings,
		},
	}
}

// shortcutLabel is how the search shortcut reads on the system.
func shortcutLabel(cx *core.Scope) string {
	if cx.Env().OS() == core.OSMacOS {
		return "⌘K"
	}
	return "Ctrl K"
}

// subpageBar is the bar of a page opened over a toolbar page: back to
// the page before, and its title.
func (sh *shell) subpageBar(cx *core.Scope) core.Node {
	return core.Show{
		When: func() bool { return len(sh.stack.Get()) > 1 },
		Then: func(cx *core.Scope) core.Node {
			return core.View{Class: "h-11 flex-row items-center gap-2 border-b border-border px-5", Children: []core.Node{
				core.View{
					TestID: "page-back", Role: core.RoleButton, Label: locales.Back(cx), OnPress: func(core.PressEvent) { sh.back() },
					Class:    "size-7 items-center justify-center rounded-md hover:bg-surface-hover",
					Children: []core.Node{core.Icon{SVG: lucide.ChevronLeft(), Class: "size-[18px] text-muted-foreground"}},
				},
				core.Text{TestID: "page-title", ContentFunc: func() string { return sh.top().title }, MaxLines: 1, Class: "flex-1 text-[14px] font-semibold text-foreground"},
			}}
		},
	}
}

// badgeKind is a source badge's look: on a card's cover, small in a line
// of text, or round on an avatar.
type badgeKind int

const (
	badgeCover badgeKind = iota
	badgeSmall
	badgeRound
)

// markColors are the colors of the plugins without one of their own,
// chosen by their ids, as their settings tiles are.
var markColors = []uint32{0xe8553a, 0x16a88e, 0xf0a800, 0x3f6fe0, 0x8b5cf6}

// markColor is p's color: its manifest's, else one by its id.
func markColor(p *plugin.Plugin) uint32 {
	if c := p.Manifest.Color; c != "" {
		if v, err := strconv.ParseUint(strings.TrimPrefix(c, "#"), 16, 32); err == nil {
			return uint32(v)
		}
	}
	h := uint32(0)
	for _, r := range p.Manifest.ID {
		h = h*31 + uint32(r)
	}
	return markColors[h%uint32(len(markColors))]
}

// lighter mixes c a quarter toward white, the top of a mark's gradient.
func lighter(c uint32) uint32 {
	mix := func(v uint32) uint32 { return v + (255-v)/4 }
	return mix(c>>16&0xff)<<16 | mix(c>>8&0xff)<<8 | mix(c&0xff)
}

// mark is the SVG of p's icon, parsed once, nil for none or one the app
// cannot read.
func (sh *shell) mark(p *plugin.Plugin) *core.SVG {
	if svg, ok := sh.marks[p]; ok {
		return svg
	}
	var svg *core.SVG
	if len(p.Icon) > 0 {
		svg, _ = core.ParseSVG(p.Icon)
	}
	sh.marks[p] = svg
	return svg
}

// badge marks what comes from p in a merged page: its icon, white on its
// color, or else its initial.
func (sh *shell) badge(p *plugin.Plugin, kind badgeKind) core.Node {
	c := markColor(p)
	box, icon, text := "size-5 rounded-md shadow-sm", "size-3", "text-[10px]"
	switch kind {
	case badgeSmall:
		box, icon, text = "size-4 rounded", "size-2.5", "text-[9px]"
	case badgeRound:
		box += " rounded-full border-2 border-white"
	}
	var child core.Node
	if svg := sh.mark(p); svg != nil {
		child = core.Icon{SVG: svg, Class: icon + " text-white"}
	} else {
		initial := "?"
		if r := []rune(strings.TrimSpace(p.Name())); len(r) > 0 {
			initial = strings.ToUpper(string(r[0]))
		}
		child = core.Text{Content: initial, Class: "font-bold text-white " + text}
	}
	return core.View{
		TestID:   "source-" + p.Manifest.ID,
		Label:    p.Name(),
		Class:    fmt.Sprintf("items-center justify-center bg-linear-to-br from-[#%06x] to-[#%06x] %s", lighter(c), c, box),
		Children: []core.Node{child},
	}
}

// sourceFilter chooses which of sources a merged page shows: all of them
// together, or one alone. It shows only when there are several.
func (sh *shell) sourceFilter(cx *core.Scope, sources func() []*plugin.Plugin) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		ps := sources()
		if len(ps) < 2 {
			return nil
		}
		chip := func(testID, label string, selected func() bool, choose func(), lead core.Node) core.Node {
			return core.View{
				TestID: testID, Role: core.RoleRadio, Label: label, Selected: selected,
				OnPress: func(core.PressEvent) { choose() },
				Class:   "group h-7 flex-row items-center gap-1.5 rounded-full border border-border bg-background py-0.5 pr-3 pl-1.5 hover:bg-surface-hover selected:border-foreground selected:bg-foreground",
				Children: []core.Node{
					lead,
					core.Text{Content: label, MaxLines: 1, Class: "text-xs font-medium text-foreground group-selected:text-background"},
				},
			}
		}
		items := []core.Node{chip("filter-all", locales.AllSources(cx), func() bool { return sh.chosen(sources()) == nil }, func() { sh.filter.Set("") },
			core.View{Class: "size-4 items-center justify-center", Children: []core.Node{
				core.Icon{SVG: lucide.Layers(), Class: "size-3.5 text-muted-foreground group-selected:text-background"},
			}})}
		for _, p := range ps {
			id := p.Manifest.ID
			items = append(items, chip("filter-"+id, p.Name(), func() bool { return sh.filter.Get() == id }, func() { sh.filter.Set(id) }, sh.badge(p, badgeSmall)))
		}
		return core.View{TestID: "source-filter", Role: core.RoleRadioGroup, Label: locales.Sources(cx), Class: "flex-row flex-wrap items-center gap-2", Children: items}
	})
}

// chosen is the one of sources the filter chose, nil for all of them.
func (sh *shell) chosen(sources []*plugin.Plugin) *plugin.Plugin {
	id := sh.filter.Get()
	if i := slices.IndexFunc(sources, func(p *plugin.Plugin) bool { return p.Manifest.ID == id }); i >= 0 && len(sources) > 1 {
		return sources[i]
	}
	return nil
}

// shown are the sources a merged page asks: the one the filter chose, or
// all of them.
func (sh *shell) shown(sources []*plugin.Plugin) []*plugin.Plugin {
	if p := sh.chosen(sources); p != nil {
		return []*plugin.Plugin{p}
	}
	return sources
}

// filterBar is a merged page's head: the source filter, and what ends
// it, such as the status tabs.
func (sh *shell) filterBar(cx *core.Scope, sources func() []*plugin.Plugin, end ...core.Node) core.Node {
	return core.Show{
		When: func() bool { return len(sources()) > 1 || len(end) > 0 },
		Then: func(cx *core.Scope) core.Node {
			return core.View{Class: "flex-row items-center justify-between gap-4 border-b border-border px-7 py-3", Children: append([]core.Node{
				core.View{Class: "flex-1", Style: core.Style{FlexShrink: 1}, Children: []core.Node{sh.sourceFilter(cx, sources)}},
			}, end...)}
		},
	}
}
