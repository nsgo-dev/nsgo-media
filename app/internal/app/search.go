package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// suggestAfter is how long typing pauses before suggest is called.
const suggestAfter = 250 * time.Millisecond

// search is a search asked of a plugin: its words and the kind of
// results, "" for all.
type search struct {
	query string
	kind  plugin.ItemKind
}

// searchPage searches p, completing the query as it is typed. A pasted
// link or ID that resolves opens what it names instead.
func (sh *shell) searchPage(cx *core.Scope, p *plugin.Plugin) core.Node {
	text := core.NewSignal(cx, "")
	asked := core.NewSignal(cx, search{})
	suggestions := core.NewSignalFunc[[]string](cx, nil, nil)
	// hovered is the suggestion the arrow keys are on, -1 for none.
	hovered := core.NewSignal(cx, -1)
	resolving := core.NewSignal(cx, false)
	// answered is the search whose first page came, after which the kinds
	// show.
	answered := core.NewSignal(cx, false)
	ctx := cx.Context()
	gen := 0
	stop := func() bool { return false }
	change := func(s string) {
		text.Set(s)
		gen++
		stop()
		hovered.Set(-1)
		q := strings.TrimSpace(s)
		if q == "" || !p.Has(plugin.ExportSuggest) {
			suggestions.Set(nil)
			return
		}
		n := gen
		stop = cx.AfterFunc(suggestAfter, func() {
			call(cx, ctx, func(ctx context.Context) ([]string, error) { return p.Suggest(ctx, plugin.SuggestParams{Query: q}) }, func(s []string, err error) {
				if n == gen && err == nil {
					suggestions.Set(s)
				}
			})
		})
	}
	ask := func(q string, kind plugin.ItemKind) {
		answered.Set(false)
		asked.Set(search{query: q, kind: kind})
	}
	submit := func(s string) {
		if i := hovered.GetUntracked(); i >= 0 && i < len(suggestions.GetUntracked()) {
			s = suggestions.GetUntracked()[i]
		}
		q := strings.TrimSpace(s)
		if q == "" {
			return
		}
		gen++
		stop()
		suggestions.Set(nil)
		hovered.Set(-1)
		text.Set(q)
		if !p.Has(plugin.ExportResolve) {
			ask(q, asked.GetUntracked().kind)
			return
		}
		n := gen
		resolving.Set(true)
		call(cx, ctx, func(ctx context.Context) (*plugin.Item, error) { return p.Resolve(ctx, plugin.ResolveParams{Text: q}) }, func(it *plugin.Item, err error) {
			if n != gen {
				return
			}
			resolving.Set(false)
			if err == nil && it != nil {
				sh.openItem(p, *it)
				return
			}
			ask(q, asked.GetUntracked().kind)
		})
	}
	clear := func() {
		change("")
		gen++
		resolving.Set(false)
		asked.Set(search{})
	}
	move := func(by int) {
		if n := len(suggestions.GetUntracked()); n > 0 {
			hovered.Set(min(max(hovered.GetUntracked()+by, -1), n-1))
		}
	}

	var busy []core.Node
	busy = append(busy, core.Show{
		When: resolving.Get,
		Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 16} },
		Else: func(cx *core.Scope) core.Node {
			return core.Show{
				When: func() bool { return text.Get() != "" },
				Then: func(cx *core.Scope) core.Node {
					return core.View{
						TestID: "clear-query", Role: core.RoleButton, Label: locales.ClearSearch(cx), OnPress: func(core.PressEvent) { clear() },
						Class:    "size-5 items-center justify-center rounded-full bg-faint-ink",
						Children: []core.Node{core.Icon{SVG: lucide.X(), Class: "size-3 text-white"}},
					}
				},
			}
		},
	})
	head := core.View{
		ClassFunc: func() string {
			if asked.Get().query != "" && !resolving.Get() {
				return "gap-4 border-b border-border px-7 pt-5 pb-4"
			}
			return "gap-4 px-7 pt-5 pb-4"
		},
		Children: []core.Node{
			core.View{Class: "max-w-[560px] flex-row", Children: []core.Node{field(lucide.Search, ui.Input{
				TestID:      "query",
				Label:       locales.Search(cx),
				Placeholder: locales.SearchPlaceholder(cx, p.Name()),
				Value:       text.Get,
				OnChange:    change,
				OnSubmit:    submit,
			}, busy...)}},
			core.Show{
				When: func() bool { return answered.Get() && asked.Get().query != "" },
				Then: func(cx *core.Scope) core.Node {
					return core.View{Class: "flex-row", Children: []core.Node{ui.Tabs[plugin.ItemKind]{
						TestID: "kind",
						Label:  locales.Kind(cx),
						Size:   ui.SizeSmall,
						Options: []ui.Option[plugin.ItemKind]{
							{Value: "", Label: locales.KindAll(cx), TestID: "kind-all"},
							{Value: plugin.KindVideo, Label: locales.KindVideos(cx), TestID: "kind-video"},
							{Value: plugin.KindUploader, Label: locales.KindUploaders(cx), TestID: "kind-uploader"},
							{Value: plugin.KindCollection, Label: locales.KindCollections(cx), TestID: "kind-collection"},
						},
						Value:    func() plugin.ItemKind { return asked.Get().kind },
						OnChange: func(k plugin.ItemKind) { ask(asked.GetUntracked().query, k) },
					}}}
				},
			},
		},
	}
	list := core.Show{
		When: func() bool { return len(suggestions.Get()) > 0 },
		Then: func(cx *core.Scope) core.Node {
			cx.OnKey("down", func() { move(1) })
			cx.OnKey("up", func() { move(-1) })
			cx.OnKey("escape", func() { suggestions.Set(nil) })
			return core.View{
				TestID: "suggestions",
				Class:  "absolute top-[64px] left-7 w-[560px] gap-0.5 rounded-xl border border-border bg-background p-1.5 shadow-xl",
				Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
					var rows []core.Node
					for i, s := range suggestions.Get() {
						rows = append(rows, core.View{
							TestID:   "suggestion-" + strconv.Itoa(i),
							Role:     core.RoleButton,
							Label:    s,
							OnPress:  func(core.PressEvent) { hovered.Set(-1); submit(s) },
							Selected: func() bool { return hovered.Get() == i },
							Class:    "h-8 flex-row items-center gap-2.5 rounded-lg px-2.5 hover:bg-surface-hover selected:bg-chip",
							Children: []core.Node{
								core.Icon{SVG: lucide.Search(), Class: "size-3.5 text-faint-ink"},
								core.Text{Content: s, MaxLines: 1, Class: "flex-1 text-[13px] text-foreground"},
							},
						})
					}
					return core.Group(rows)
				})},
			}
		},
	}
	body := core.Dynamic(func(cx *core.Scope) core.Node {
		a := asked.Get()
		if resolving.Get() {
			return core.View{Class: "flex-1"}
		}
		if a.query == "" {
			return state(stateQuiet, lucide.Search, locales.SearchTitle(cx, p.Name()), locales.SearchHint(cx))
		}
		return sh.results(cx, p, a, func() { answered.Set(true) })
	})
	return core.View{
		Style:    core.Style{FlexGrow: 1, FlexShrink: 1},
		Children: []core.Node{head, body, list},
	}
}

// results are what p answered for a search, or that nothing matched;
// done hears that its first page came.
func (sh *shell) results(cx *core.Scope, p *plugin.Plugin, a search, done func()) core.Node {
	empty := core.NewSignal(cx, false)
	page := sh.sectionsPage(cx, p, pageSearch, func(ctx context.Context, cursor string) (*plugin.Page, error) {
		pg, err := p.Search(ctx, plugin.SearchParams{Query: a.query, Kind: a.kind, Cursor: cursor})
		if err == nil && cursor == "" {
			none := true
			for _, s := range pg.Sections {
				if len(s.Items) > 0 {
					none = false
				}
			}
			cx.Post(func() {
				empty.Set(none)
				done()
			})
		}
		return pg, err
	})
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{
		page,
		core.Show{
			When: empty.Get,
			Then: func(cx *core.Scope) core.Node {
				return state(stateQuiet, lucide.SearchX, locales.NoResults(cx), locales.NothingMatches(cx, a.query))
			},
		},
	}}
}
