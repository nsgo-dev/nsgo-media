package app

import (
	"context"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// The pieces the pages share, as design/components draws them.

// stateKind is the look of a page's state: a quiet one, an error, or one
// asking the viewer to sign in or set something up.
type stateKind int

const (
	stateQuiet stateKind = iota
	stateError
	stateAsk
)

// state fills a page with why it shows nothing: an icon, a title, a
// message and an action.
func state(kind stateKind, icon func() *core.SVG, title, message string, action ...core.Node) core.Node {
	badge, tint := "bg-chip", "text-faint-ink"
	switch kind {
	case stateError:
		badge, tint = "bg-error-soft", "text-error-icon"
	case stateAsk:
		badge, tint = "bg-warn-soft", "text-warn-icon"
	}
	children := []core.Node{
		core.View{Class: "size-14 items-center justify-center rounded-2xl " + badge, Children: []core.Node{
			core.Icon{SVG: icon(), Class: "size-6 " + tint},
		}},
		core.View{Class: "max-w-[360px] items-center gap-1", Children: []core.Node{
			core.Text{TestID: "state-title", Content: title, Class: "text-center text-[15px] font-semibold text-foreground"},
			core.Text{TestID: "message", Content: message, Class: "text-center text-[13px] leading-relaxed text-muted-foreground"},
		}},
	}
	if len(action) > 0 && action[0] != nil {
		children = append(children, core.View{Class: "mt-1 flex-row gap-2", Children: action})
	}
	return core.View{TestID: "state", Class: "flex-1 items-center justify-center gap-3 px-10 py-16", Children: children}
}

// sectionHeader titles a section, with More when it has the rest of its
// items on a page of their own.
func sectionHeader(cx *core.Scope, title, testID string, more func()) core.Node {
	children := []core.Node{core.Text{Content: title, MaxLines: 1, Class: "flex-1 text-[17px] font-semibold text-foreground"}}
	if more != nil {
		children = append(children, core.View{
			TestID:  testID,
			Role:    core.RoleButton,
			Label:   locales.More(cx),
			OnPress: func(core.PressEvent) { more() },
			Class:   "flex-row items-center gap-0.5 rounded-md px-2 py-1 hover:bg-surface-hover",
			Children: []core.Node{
				core.Text{Content: locales.More(cx), Class: "text-[13px] font-medium text-accent"},
				core.Icon{SVG: lucide.ChevronRight(), Class: "size-3.5 text-accent"},
			},
		})
	}
	return core.View{Class: "h-7 flex-row items-center gap-2", Children: children}
}

// cardWidth is a card's, and cardRow the classes of a row of cards.
const (
	cardWidth = "w-[213px]"
	cardRow   = "flex-row flex-wrap gap-x-5 gap-y-6"
)

// skeletonCard stands in for a card while its page loads.
func skeletonCard() core.Node {
	return core.View{Class: cardWidth + " gap-2", Children: []core.Node{
		core.View{Class: "h-[120px] rounded-xl bg-skeleton"},
		core.View{Class: "h-3 w-11/12 rounded bg-skeleton"},
		core.View{Class: "h-3 w-3/5 rounded bg-chip"},
	}}
}

// skeletonCards is a row of n skeleton cards.
func skeletonCards(n int) core.Node {
	cards := make([]core.Node, n)
	for i := range cards {
		cards[i] = skeletonCard()
	}
	return core.View{TestID: "loading", Class: cardRow, Children: cards}
}

// skeletonLine is a heading's place while it loads.
func skeletonLine(width string) core.Node {
	return core.View{Class: "h-5 rounded-md bg-skeleton " + width}
}

// bar is a progress bar along a picture's bottom, its fill a share of
// it.
func bar(height string, share func() float64) core.Node {
	return core.View{
		ClassFunc: func() string {
			if share() <= 0 {
				return "hidden"
			}
			return "absolute right-0 bottom-0 left-0 bg-white/40 " + height
		},
		Children: []core.Node{core.View{
			Class:     "h-full bg-accent",
			StyleFunc: func() core.Style { return core.Style{Width: core.Pct(float32(min(share(), 1) * 100))} },
		}},
	}
}

// card is an item of a page, which opens what it names.
func (sh *shell) card(cx *core.Scope, p *plugin.Plugin, it plugin.Item) core.Node {
	open := func(core.PressEvent) { sh.openItem(p, it) }
	id := "item-" + string(it.Kind) + "-" + it.ID
	switch it.Kind {
	case plugin.KindUploader:
		children := []core.Node{
			sh.cover(cx, it.Image, "size-[88px] rounded-full border border-black/5 bg-well shadow-sm"),
			core.Text{Content: it.Title, MaxLines: 1, Class: "text-center text-[13px] font-medium text-foreground"},
		}
		if it.Subtitle != "" {
			children = append(children, core.Text{Content: it.Subtitle, MaxLines: 1, Class: "text-center text-xs text-muted-foreground"})
		}
		return core.View{TestID: id, Role: core.RoleButton, Label: it.Title, OnPress: open, Class: "w-[132px] items-center gap-2 py-1", Children: children}
	case plugin.KindLink:
		return core.View{
			TestID: id, Role: core.RoleButton, Label: it.Title, OnPress: open,
			Class: cardWidth + " h-11 flex-row items-center gap-2.5 rounded-xl border border-border bg-background px-3.5 hover:bg-surface-hover",
			Children: []core.Node{
				core.Icon{SVG: lucide.List(), Class: "size-4 text-faint-ink"},
				core.Text{Content: it.Title, MaxLines: 1, Class: "flex-1 text-[13px] font-medium text-foreground"},
				core.Icon{SVG: lucide.ChevronRight(), Class: "size-4 text-faint-ink"},
			},
		}
	}
	thumb := []core.Node{sh.cover(cx, it.Image, "size-full")}
	if it.Duration > 0 {
		thumb = append(thumb, core.Text{
			Content: clock(it.Duration),
			Class:   "absolute right-1.5 bottom-1.5 rounded-md bg-black/70 px-1.5 py-0.5 text-[11px] font-medium text-white",
			Style:   core.Style{FontVariant: core.FontTabularNums},
		})
		thumb = append(thumb, bar("h-[3px]", func() float64 { return sh.watched(p, it.ID, it.Duration) }))
	}
	info := []core.Node{core.Text{Content: it.Title, MaxLines: 2, Class: "text-[13px] leading-snug font-medium text-foreground"}}
	if it.Subtitle != "" {
		info = append(info, core.Text{Content: it.Subtitle, MaxLines: 1, Class: "text-xs text-muted-foreground"})
	}
	return core.View{TestID: id, Role: core.RoleButton, Label: it.Title, OnPress: open, Class: cardWidth + " gap-2", Children: []core.Node{
		core.View{Class: "h-[120px] overflow-hidden rounded-xl bg-well shadow-sm", Children: thumb},
		core.View{Class: "gap-0.5 px-0.5", Children: info},
	}}
}

// watched is how far the viewer got in a video of p, from the resume
// positions kept, as a share of its length.
func (sh *shell) watched(p *plugin.Plugin, video string, length float64) float64 {
	sh.played.Get()
	if sh.positions == nil || length <= 0 {
		return 0
	}
	_, pos, ok := sh.positions.Latest(videoKey(p, video))
	if !ok {
		return 0
	}
	return pos.Seconds() / length
}

// cover is a plugin's picture filling a box of classes, the box's fill
// while it downloads, when it fails and when there is none.
func (sh *shell) cover(cx *core.Scope, src *plugin.Source, class string) core.Node {
	data := core.NewSignalFunc[[]byte](cx, nil, nil)
	if src != nil {
		call(cx, cx.Context(), func(ctx context.Context) ([]byte, error) { return sh.pictures.get(ctx, *src) }, func(b []byte, err error) {
			if err == nil {
				data.Set(b)
			}
		})
	}
	return core.View{Class: "overflow-hidden " + class, Children: []core.Node{
		core.Image{
			SourceFunc: func() core.ImageSource { return core.ImageSource{Data: data.Get()} },
			ResizeMode: core.Cover,
			Class:      "absolute inset-0",
		},
	}}
}

// tile is a plugin's initial on a tile of its colors, as the settings
// list it.
func tile(name, class, text, colors string) core.Node {
	initial := "?"
	if r := []rune(strings.TrimSpace(name)); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	return core.View{
		Class:    "items-center justify-center bg-linear-to-br shadow-sm " + colors + " " + class,
		Children: []core.Node{core.Text{Content: initial, Class: "font-bold text-white " + text}},
	}
}

// tileColors are the plugins' tiles' gradients, chosen by their ids; a
// refused plugin's is grey.
var tileColors = []string{
	"from-[#FF8A6B] to-[#E8553A]",
	"from-[#3BD6B8] to-[#16A88E]",
	"from-[#FFD25C] to-[#F0A800]",
	"from-[#7AA7FF] to-[#3F6FE0]",
	"from-[#C39BFF] to-[#8B5CF6]",
}

const greyTile = "from-zinc-300 to-zinc-400"

func colorsOf(id string) string {
	h := uint32(0)
	for _, r := range id {
		h = h*31 + uint32(r)
	}
	return tileColors[h%uint32(len(tileColors))]
}

// group is a titled group of a settings page's rows.
func group(title string, children ...core.Node) core.Node {
	return core.View{Class: "gap-2", Children: []core.Node{
		core.Text{Content: title, Class: "px-1 text-xs font-semibold text-muted-foreground"},
		core.View{Class: "overflow-hidden rounded-xl border border-border bg-background", Children: children},
	}}
}

// row is a settings row: its title, a detail under it and its control.
func row(testID, title, detail string, last bool, control core.Node) core.Node {
	text := []core.Node{core.Text{Content: title, Class: "text-[13px] font-medium text-foreground"}}
	if detail != "" {
		text = append(text, core.Text{Content: detail, Class: "text-xs text-muted-foreground"})
	}
	class := "min-h-[48px] flex-row items-center gap-3 px-4 py-2.5"
	if !last {
		class += " border-b border-border"
	}
	return core.View{TestID: testID, Class: class, Children: []core.Node{
		core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: text},
		core.View{Class: "flex-row items-center gap-2", Children: []core.Node{control}},
	}}
}

// hostChip is a host a plugin may reach.
func hostChip(host string) core.Node {
	return core.View{Class: "flex-row items-center gap-1.5 rounded-md bg-chip px-2 py-1", Children: []core.Node{
		core.Icon{SVG: lucide.Globe(), Class: "size-3 text-muted-foreground"},
		core.Text{Content: host, Class: "text-xs font-medium text-foreground"},
	}}
}

// banner is a line of a page in a tinted box: a warning or an error.
func banner(testID string, warn bool, icon func() *core.SVG, title, message string, action core.Node) core.Node {
	box, tint, ink := "border-error-line bg-error-soft", "text-error-icon", "text-error-ink"
	if warn {
		box, tint, ink = "border-warn-line bg-warn-soft", "text-warn-icon", "text-warn-ink"
	}
	text := []core.Node{}
	if title != "" {
		text = append(text, core.Text{Content: title, Class: "text-[13px] font-semibold " + ink})
	}
	text = append(text, core.Text{Content: message, Class: "text-[13px] " + ink})
	children := []core.Node{
		core.Icon{SVG: icon(), Class: "mt-0.5 size-4 " + tint},
		core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: text},
	}
	if action != nil {
		children = append(children, action)
	}
	return core.View{TestID: testID, Class: "flex-row items-start gap-3 rounded-xl border px-4 py-3 " + box, Children: children}
}

// retryButton tries a failed call again.
func retryButton(cx *core.Scope, retry func()) core.Node {
	return ui.Button{TestID: "retry", Text: locales.Retry(cx), Variant: ui.ButtonOutline, OnPress: retry}
}
