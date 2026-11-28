package app

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
	"github.com/nsgo-dev/nsgo-media/app/locales"
)

// suggestAfter is how long typing pauses before suggest is called.
const suggestAfter = 250 * time.Millisecond

// maxSuggestions bounds the completions the plugins answer together.
const maxSuggestions = 8

// search is a search asked of the plugins: its words and the kind of
// results, "" for all.
type search struct {
	query string
	kind  plugin.ItemKind
}

// resolved is what a plugin resolved a pasted link or ID to.
type resolved struct {
	p  *plugin.Plugin
	it *plugin.Item
}

// suggestAll completes q with the answers of every plugin of ps, in
// their order, each once.
func suggestAll(ctx context.Context, ps []*plugin.Plugin, q string) []string {
	answers := make([][]string, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			if s, err := p.Suggest(ctx, plugin.SuggestParams{Query: q}); err == nil {
				answers[i] = s
			}
		})
	}
	wg.Wait()
	var out []string
	for _, a := range answers {
		for _, s := range a {
			if !slices.Contains(out, s) && len(out) < maxSuggestions {
				out = append(out, s)
			}
		}
	}
	return out
}

// resolveAll asks every plugin of ps what text names: the first in their
// order to answer one.
func resolveAll(ctx context.Context, ps []*plugin.Plugin, text string) resolved {
	answers := make([]*plugin.Item, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			if it, err := p.Resolve(ctx, plugin.ResolveParams{Text: text}); err == nil {
				answers[i] = it
			}
		})
	}
	wg.Wait()
	for i, it := range answers {
		if it != nil {
			return resolved{ps[i], it}
		}
	}
	return resolved{}
}

// searchPage searches every enabled plugin at once, or the one the
// filter chose, completing the query as it is typed. A pasted link or ID
// that a plugin resolves opens what it names instead.
func (sh *shell) searchPage(cx *core.Scope) core.Node {
	sources := func() []*plugin.Plugin { return sh.sources(plugin.ExportSearch) }
	having := func(export string) []*plugin.Plugin {
		var ps []*plugin.Plugin
		for _, p := range sh.shown(core.Untracked(cx, sources)) {
			if p.Has(export) {
				ps = append(ps, p)
			}
		}
		return ps
	}
	text := core.NewSignal(cx, "")
	asked := core.NewSignal(cx, search{})
	suggestions := core.NewSignalFunc[[]string](cx, nil, nil)
	// hovered is the suggestion the arrow keys are on, -1 for none.
	hovered := core.NewSignal(cx, -1)
	resolving := core.NewSignal(cx, false)
	ref := &core.Ref{}
	searches := sh.searches.GetUntracked()
	cx.Effect(func() {
		if n := sh.searches.Get(); n != searches {
			searches = n
			ref.Focus()
		}
	})
	ctx := cx.Context()
	gen := 0
	stop := func() bool { return false }
	change := func(s string) {
		text.Set(s)
		gen++
		stop()
		hovered.Set(-1)
		q := strings.TrimSpace(s)
		ps := having(plugin.ExportSuggest)
		if q == "" || len(ps) == 0 {
			suggestions.Set(nil)
			return
		}
		n := gen
		stop = cx.AfterFunc(suggestAfter, func() {
			call(cx, ctx, func(ctx context.Context) ([]string, error) { return suggestAll(ctx, ps, q), nil }, func(s []string, err error) {
				if n == gen && err == nil {
					suggestions.Set(s)
				}
			})
		})
	}
	ask := func(q string, kind plugin.ItemKind) { asked.Set(search{query: q, kind: kind}) }
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
		ps := having(plugin.ExportResolve)
		if len(ps) == 0 {
			ask(q, asked.GetUntracked().kind)
			return
		}
		n := gen
		resolving.Set(true)
		call(cx, ctx, func(ctx context.Context) (resolved, error) { return resolveAll(ctx, ps, q), nil }, func(r resolved, _ error) {
			if n != gen {
				return
			}
			resolving.Set(false)
			if r.it != nil {
				sh.openItem(r.p, *r.it)
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
	kinds := core.Show{
		When: func() bool { return asked.Get().query != "" && !resolving.Get() },
		Then: func(cx *core.Scope) core.Node {
			return ui.Tabs[plugin.ItemKind]{
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
			}
		},
	}
	head := core.View{
		ClassFunc: func() string {
			if asked.Get().query != "" && !resolving.Get() {
				return "gap-3 border-b border-border px-7 pt-5 pb-4"
			}
			return "gap-3 px-7 pt-5 pb-4"
		},
		Children: []core.Node{
			core.View{Class: "flex-row items-center justify-between gap-4", Children: []core.Node{
				core.View{Class: "max-w-[560px] flex-1 flex-row", Style: core.Style{FlexShrink: 1}, Children: []core.Node{field(lucide.Search, ui.Input{
					TestID:      "query",
					Ref:         ref,
					AutoFocus:   true,
					Label:       locales.Search(cx),
					Placeholder: locales.SearchPlaceholderAll(cx),
					Value:       text.Get,
					OnChange:    change,
					OnSubmit:    submit,
				}, busy...)}},
				kinds,
			}},
			sh.sourceFilter(cx, sources),
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
				Class:  "absolute top-[66px] left-7 w-[560px] gap-0.5 rounded-xl border border-border bg-background p-1.5 shadow-xl",
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
			return state(stateQuiet, lucide.Search, locales.SearchAllTitle(cx), locales.SearchHint(cx))
		}
		shown := sh.shown(sources())
		return sh.sectionsPage(cx, shown, pageSearch, func(p *plugin.Plugin, ctx context.Context, cursor string) (*plugin.Page, error) {
			return p.Search(ctx, plugin.SearchParams{Query: a.query, Kind: a.kind, Cursor: cursor})
		}, nil, func(cx *core.Scope) core.Node {
			return state(stateQuiet, lucide.SearchX, locales.NoResults(cx), locales.NothingMatches(cx, a.query))
		})
	})
	return core.View{
		Style:    core.Style{FlexGrow: 1, FlexShrink: 1},
		Children: []core.Node{head, body, list},
	}
}
