package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// suggestAfter is how long typing pauses before suggest is called.
const suggestAfter = 250 * time.Millisecond

// searchPage searches p, completing the query as it is typed. A pasted
// link or ID that resolves opens what it names instead.
func (sh *shell) searchPage(cx *core.Scope, p *plugin.Plugin) core.Node {
	text := core.NewSignal(cx, "")
	query := core.NewSignal(cx, "")
	suggestions := core.NewSignalFunc[[]string](cx, nil, nil)
	resolving := core.NewSignal(cx, false)
	ctx := cx.Context()
	gen := 0
	stop := func() bool { return false }
	change := func(s string) {
		text.Set(s)
		gen++
		stop()
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
	submit := func(s string) {
		q := strings.TrimSpace(s)
		if q == "" {
			return
		}
		gen++
		stop()
		suggestions.Set(nil)
		text.Set(q)
		if !p.Has(plugin.ExportResolve) {
			query.Set(q)
			return
		}
		resolving.Set(true)
		call(cx, ctx, func(ctx context.Context) (*plugin.Item, error) { return p.Resolve(ctx, plugin.ResolveParams{Text: q}) }, func(it *plugin.Item, err error) {
			resolving.Set(false)
			if err == nil && it != nil {
				sh.openItem(p, *it)
				return
			}
			query.Set(q)
		})
	}
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1},
		Children: []core.Node{
			core.View{
				Style: core.Style{Gap: 4, Padding: core.Edges{Top: 16, Left: 16, Right: 16}},
				Children: []core.Node{
					core.View{Style: core.Style{Direction: core.Row, Gap: 8, MaxWidth: core.Pt(640)}, Children: []core.Node{
						ui.Input{
							TestID:      "query",
							Label:       locales.Search(cx),
							Placeholder: locales.SearchPlaceholder(cx),
							Value:       text.Get,
							OnChange:    change,
							OnSubmit:    submit,
							Style:       core.Style{FlexGrow: 1, FlexShrink: 1},
						},
						ui.Button{TestID: "search", Text: locales.Search(cx), Busy: resolving.Get, OnPress: func() { submit(text.GetUntracked()) }},
					}},
					core.Dynamic(func(cx *core.Scope) core.Node {
						var rows []core.Node
						for i, s := range suggestions.Get() {
							rows = append(rows, ui.Button{TestID: "suggestion-" + strconv.Itoa(i), Text: s, Variant: ui.ButtonGhost, OnPress: func() { submit(s) }})
						}
						return core.View{Style: core.Style{AlignItems: core.AlignStart, MaxWidth: core.Pt(640)}, Children: rows}
					}),
				},
			},
			core.Dynamic(func(cx *core.Scope) core.Node {
				q := query.Get()
				if q == "" {
					return nil
				}
				return sh.sectionsPage(cx, p, func(ctx context.Context, cursor string) (*plugin.Page, error) {
					return p.Search(ctx, plugin.SearchParams{Query: q, Cursor: cursor})
				})
			}),
		},
	}
}
