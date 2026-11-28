package app

import (
	"context"
	"errors"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
	"github.com/nsgo-dev/nsgo-media/app/locales"
)

// A merged page asks each of its plugins for its pages, a pager each, and
// shows their answers together: their sections of one title as one, and
// each card marked with its plugin.

// pager loads a plugin's pages of T one after another, each at the cursor
// the one before answered.
type pager[T any] struct {
	// cx is the page's scope, which the calls answer in, and ctx its
	// context, taken as it rendered.
	cx      *core.Scope
	ctx     context.Context
	p       *plugin.Plugin
	load    func(ctx context.Context, cursor string) (T, error)
	next    func(T) string
	pages   *core.Signal[[]T]
	loading *core.Signal[bool]
	failed  *core.Signal[error]
	gen     int
}

func newPager[T any](cx *core.Scope, p *plugin.Plugin, load func(ctx context.Context, cursor string) (T, error), next func(T) string) *pager[T] {
	return &pager[T]{cx: cx, ctx: cx.Context(), p: p, load: load, next: next, pages: core.NewSignalFunc[[]T](cx, nil, nil),
		loading: core.NewSignal(cx, false), failed: core.NewSignalFunc[error](cx, nil, nil)}
}

// fetch loads the page at cursor, "" starting over.
func (pg *pager[T]) fetch(cursor string) {
	cx := pg.cx
	pg.gen++
	n := pg.gen
	pg.loading.Set(true)
	pg.failed.Set(nil)
	call(cx, pg.ctx, func(ctx context.Context) (T, error) { return pg.load(ctx, cursor) }, func(page T, err error) {
		if n != pg.gen {
			return
		}
		pg.loading.Set(false)
		if err != nil {
			pg.failed.Set(err)
			return
		}
		if cursor == "" {
			pg.pages.Set([]T{page})
			return
		}
		pg.pages.Update(func(ps []T) []T { return append(ps[:len(ps):len(ps)], page) })
	})
}

// cursor is where the next page starts, "" for none.
func (pg *pager[T]) cursor() string {
	ps := pg.pages.Get()
	if len(ps) == 0 {
		return ""
	}
	return pg.next(ps[len(ps)-1])
}

// started reports whether its first page came.
func (pg *pager[T]) started() bool { return len(pg.pages.Get()) > 0 }

// pagers are a merged page's.
type pagers[T any] []*pager[T]

// start loads every first page, again whenever its plugin's account
// signs in or out, or follows change when follows.
func (ps pagers[T]) start(cx *core.Scope, sh *shell, follows bool) {
	for _, pg := range ps {
		a := sh.account(pg.p)
		cx.Effect(func() {
			a.epoch.Get()
			if follows {
				a.follows.Get()
			}
			cx.Untrack(func() { pg.fetch("") })
		})
	}
}

// more loads the next page of every pager with one, unless one is
// loading or failed.
func (ps pagers[T]) more(cx *core.Scope) {
	for _, pg := range ps {
		if pg.loading.GetUntracked() || pg.failed.GetUntracked() != nil {
			return
		}
	}
	for _, pg := range ps {
		if c := core.Untracked(cx, pg.cursor); c != "" {
			pg.fetch(c)
		}
	}
}

func (ps pagers[T]) any(f func(*pager[T]) bool) bool {
	for _, pg := range ps {
		if f(pg) {
			return true
		}
	}
	return false
}

// feed shows what pagers answered as body lays it out: their loading
// and failures, with the next pages on request or once scrolled to the
// end. empty says whether they answered nothing, and nothing is what
// shows then, nil for nothing.
func feed[T any](cx *core.Scope, sh *shell, ps pagers[T], loading core.Node, empty func() bool, nothing func(*core.Scope) core.Node, body func(*core.Scope) core.Node) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		started := ps.any(func(pg *pager[T]) bool { return pg.started() })
		waiting := ps.any(func(pg *pager[T]) bool { return !pg.started() && pg.failed.Get() == nil })
		switch {
		case len(ps) == 0:
			return nothing(cx)
		case !started && !waiting:
			// Every plugin failed: the first one's failure says what to do.
			pg := ps[0]
			return sh.failure(cx, pg.p, pg.failed.Get(), func() {
				for _, pg := range ps {
					pg.fetch("")
				}
			})
		case !started:
			return loading
		case !waiting && empty() && !ps.any(func(pg *pager[T]) bool { return pg.failed.Get() != nil }):
			return nothing(cx)
		}
		return ui.ScrollView{
			TestID:       "sections",
			Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
			ContentStyle: core.Style{Padding: core.Symmetric(20, 28), Gap: 28},
			OnScroll: func(e core.ScrollEvent) {
				if e.Offset.Y+e.Size.Height >= e.ContentSize.Height-240 {
					ps.more(cx)
				}
			},
			Children: []core.Node{
				core.Dynamic(body),
				core.Dynamic(func(cx *core.Scope) core.Node {
					var nodes []core.Node
					for _, pg := range ps {
						if pg.failed.Get() == nil {
							continue
						}
						cursor := ""
						if pg.started() {
							cursor = core.Untracked(cx, pg.cursor)
						}
						err, p := pg.failed.Get(), pg.p
						text, action := locales.CouldNotAnswerShort(cx, p.Name()), retryButton(cx, func() { pg.fetch(cursor) })
						switch {
						case errors.Is(err, plugin.ErrSignIn):
							text = locales.SignInTo(cx, p.Name())
							action = ui.Button{TestID: "banner-sign-in", Text: locales.SignIn(cx), Variant: ui.ButtonOutline, Busy: sh.account(p).busy.Get, OnPress: func() { sh.signIn(p) }}
						case errors.Is(err, plugin.ErrSetup):
							text = locales.NeedsSetup(cx, p.Name())
							action = ui.Button{TestID: "banner-settings", Text: locales.OpenSettings(cx), Variant: ui.ButtonOutline, OnPress: func() { sh.openPlugin(p) }}
						}
						nodes = append(nodes, core.View{TestID: "more-failed", Class: "flex-row items-center justify-center gap-3 rounded-xl bg-error-soft px-4 py-3", Children: []core.Node{
							sh.badge(p, badgeSmall),
							core.Text{Content: text, Class: "text-[13px] text-error-ink", Style: core.Style{FlexShrink: 1}},
							action,
						}})
					}
					return core.Group(nodes)
				}),
				core.Show{
					When: func() bool { return ps.any(func(pg *pager[T]) bool { return pg.loading.Get() }) },
					Then: func(*core.Scope) core.Node { return skeletonCards(5) },
				},
				core.Show{
					When: func() bool {
						return !ps.any(func(pg *pager[T]) bool { return pg.loading.Get() || pg.failed.Get() != nil }) &&
							ps.any(func(pg *pager[T]) bool { return pg.cursor() != "" })
					},
					Then: func(cx *core.Scope) core.Node {
						return core.View{Class: "items-center pt-2", Children: []core.Node{
							ui.Button{TestID: "load-more", Text: locales.LoadMore(cx), Variant: ui.ButtonOutline, OnPress: func() { ps.more(cx) }},
						}}
					},
				},
			},
		}
	})
}

// entry is an item of a plugin's.
type entry struct {
	p  *plugin.Plugin
	it plugin.Item
}

// section is a titled row of the items of one or several plugins, with
// More when one plugin has the rest of its items on a page of its own.
type section struct {
	title string
	more  string
	p     *plugin.Plugin
	items []entry
}

// merge joins the sections of the plugins' pages: those of one title are
// one, their items taken a plugin at a time in turn, in the order the
// first of them came.
func merge(ps pagers[*plugin.Page]) []section {
	type part struct {
		p     *plugin.Plugin
		items []plugin.Item
		more  string
	}
	var titles []string
	parts := map[string][]part{}
	for _, pg := range ps {
		for _, page := range pg.pages.Get() {
			for _, s := range page.Sections {
				if len(s.Items) == 0 {
					continue
				}
				if _, ok := parts[s.Title]; !ok {
					titles = append(titles, s.Title)
				}
				list := parts[s.Title]
				if n := len(list); n > 0 && list[n-1].p == pg.p {
					list[n-1].items = append(list[n-1].items, s.Items...)
				} else {
					list = append(list, part{p: pg.p, items: s.Items, more: s.More})
				}
				parts[s.Title] = list
			}
		}
	}
	var out []section
	for _, title := range titles {
		list := parts[title]
		s := section{title: title}
		if len(list) == 1 {
			s.p, s.more = list[0].p, list[0].more
		}
		for i := 0; ; i++ {
			added := false
			for _, pt := range list {
				if i < len(pt.items) {
					s.items = append(s.items, entry{p: pt.p, it: pt.items[i]})
					added = true
				}
			}
			if !added {
				break
			}
		}
		out = append(out, s)
	}
	return out
}

// sectionsView lays out merged sections.
func (sh *shell) sectionsView(cx *core.Scope, sections []section) core.Node {
	var nodes []core.Node
	for _, s := range sections {
		nodes = append(nodes, sh.section(cx, s))
	}
	return core.View{Class: "gap-7", Children: nodes}
}

// section is a titled row of cards, with More for the rest of them; a
// section without a title shows its cards alone.
func (sh *shell) section(cx *core.Scope, s section) core.Node {
	var children []core.Node
	if s.title != "" || s.more != "" {
		var more func()
		if s.more != "" {
			more = func() { sh.open(route{kind: routeBrowse, plugin: s.p, id: s.more, title: s.title}) }
		}
		children = append(children, sectionHeader(cx, s.title, "more-"+s.more, more))
	}
	var cards []core.Node
	for _, e := range s.items {
		cards = append(cards, sh.card(cx, e.p, e.it))
	}
	children = append(children, core.View{Class: cardRow, Children: cards})
	return core.View{Class: "gap-3", Children: children}
}

// empty reports whether the pages hold no item.
func emptyPages(ps pagers[*plugin.Page]) bool {
	for _, pg := range ps {
		for _, page := range pg.pages.Get() {
			for _, s := range page.Sections {
				if len(s.Items) > 0 {
					return false
				}
			}
		}
	}
	return true
}

// sectionsPage shows the sections of the plugins' pages load answers,
// merged; head goes before them, such as continuing what was watched.
func (sh *shell) sectionsPage(cx *core.Scope, plugins []*plugin.Plugin, kind pageKind, load func(p *plugin.Plugin, ctx context.Context, cursor string) (*plugin.Page, error), head func(*core.Scope) []section, nothing func(*core.Scope) core.Node) core.Node {
	var ps pagers[*plugin.Page]
	for _, p := range plugins {
		ps = append(ps, newPager(cx, p, func(ctx context.Context, cursor string) (*plugin.Page, error) { return load(p, ctx, cursor) },
			func(pg *plugin.Page) string { return pg.Next }))
	}
	ps.start(cx, sh, false)
	if len(ps) == 1 && kind == pageBrowse {
		// A plugin's own page takes its title.
		cx.Effect(func() {
			if pages := ps[0].pages.Get(); len(pages) > 0 {
				cx.Untrack(func() { sh.retitle(pages[0].Title) })
			}
		})
	}
	extra := func(cx *core.Scope) []section {
		if head == nil {
			return nil
		}
		return head(cx)
	}
	empty := func() bool {
		return emptyPages(ps) && len(core.Untracked(cx, func() []section { return extra(cx) })) == 0
	}
	return feed(cx, sh, ps, pageLoading(kind), empty, nothing, func(cx *core.Scope) core.Node {
		return sh.sectionsView(cx, append(extra(cx), merge(ps)...))
	})
}
