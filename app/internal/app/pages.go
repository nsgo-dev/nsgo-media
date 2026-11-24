package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// loader loads a page of sections at a cursor, "" for the first.
type loader func(ctx context.Context, cursor string) (*plugin.Page, error)

// pageKind is what a page of sections is, for its loading and empty
// states.
type pageKind int

const (
	pageHome pageKind = iota
	pageBrowse
	pageSearch
)

// sectionsPage shows the sections load answers, with the pages after it
// on request or once scrolled to the end, again after the viewer signs in
// or out.
func (sh *shell) sectionsPage(cx *core.Scope, p *plugin.Plugin, kind pageKind, load loader) core.Node {
	pages := core.NewSignalFunc[[]*plugin.Page](cx, nil, nil)
	loading := core.NewSignal(cx, false)
	failed := core.NewSignalFunc[error](cx, nil, nil)
	ctx := cx.Context()
	gen := 0
	fetch := func(cursor string) {
		gen++
		n := gen
		loading.Set(true)
		failed.Set(nil)
		call(cx, ctx, func(ctx context.Context) (*plugin.Page, error) { return load(ctx, cursor) }, func(pg *plugin.Page, err error) {
			if n != gen {
				return
			}
			loading.Set(false)
			if err != nil {
				failed.Set(err)
				return
			}
			if len(pages.GetUntracked()) == 0 && kind != pageSearch {
				sh.retitle(pg.Title)
			}
			pages.Update(func(ps []*plugin.Page) []*plugin.Page { return append(ps[:len(ps):len(ps)], pg) })
		})
	}
	epoch := sh.account(p).epoch
	cx.Effect(func() {
		epoch.Get()
		cx.Untrack(func() {
			pages.Set(nil)
			fetch("")
		})
	})
	next := func() string {
		ps := pages.Get()
		if len(ps) == 0 {
			return ""
		}
		return ps[len(ps)-1].Next
	}
	more := func() {
		if !loading.GetUntracked() && failed.GetUntracked() == nil {
			if c := core.Untracked(cx, next); c != "" {
				fetch(c)
			}
		}
	}
	started := func() bool { return len(pages.Get()) > 0 }
	empty := func() bool {
		for _, pg := range pages.Get() {
			for _, s := range pg.Sections {
				if len(s.Items) > 0 {
					return false
				}
			}
		}
		return true
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch {
		case !started() && failed.Get() != nil:
			return sh.failure(cx, p, failed.Get(), func() { fetch("") })
		case !started():
			return pageLoading(kind)
		case empty():
			if kind == pageSearch {
				return nil
			}
			msg := locales.PageEmpty(cx)
			if kind == pageHome {
				msg = locales.HomeEmpty(cx, p.Name())
			}
			return state(stateQuiet, lucide.Inbox, locales.NothingHere(cx), msg)
		}
		return ui.ScrollView{
			TestID:       "sections",
			Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
			ContentStyle: core.Style{Padding: core.Symmetric(24, 28), Gap: 32},
			OnScroll: func(e core.ScrollEvent) {
				if e.Offset.Y+e.Size.Height >= e.ContentSize.Height-240 {
					more()
				}
			},
			Children: []core.Node{
				core.Dynamic(func(cx *core.Scope) core.Node {
					var nodes []core.Node
					for _, pg := range pages.Get() {
						for _, s := range pg.Sections {
							if len(s.Items) > 0 {
								nodes = append(nodes, sh.section(cx, p, s))
							}
						}
					}
					return core.View{Class: "gap-8", Children: nodes}
				}),
				core.Show{
					When: loading.Get,
					Then: func(*core.Scope) core.Node { return skeletonCards(4) },
				},
				core.Show{
					When: func() bool { return failed.Get() != nil },
					Then: func(cx *core.Scope) core.Node {
						return core.View{TestID: "more-failed", Class: "flex-row items-center justify-center gap-3 rounded-xl bg-error-soft px-4 py-3", Children: []core.Node{
							core.Icon{SVG: lucide.CircleAlert(), Class: "size-4 text-error-icon"},
							core.Text{Content: locales.CouldNotAnswerShort(cx, p.Name()), Class: "text-[13px] text-error-ink", Style: core.Style{FlexShrink: 1}},
							retryButton(cx, func() { fetch(core.Untracked(cx, next)) }),
						}}
					},
				},
				core.Show{
					When: func() bool { return !loading.Get() && failed.Get() == nil && next() != "" },
					Then: func(cx *core.Scope) core.Node {
						return core.View{Class: "items-center pt-2", Children: []core.Node{
							ui.Button{TestID: "load-more", Text: locales.LoadMore(cx), Variant: ui.ButtonOutline, OnPress: more},
						}}
					},
				},
			},
		}
	})
}

// pageLoading stands in for a page of kind while it loads.
func pageLoading(kind pageKind) core.Node {
	var children []core.Node
	switch kind {
	case pageHome:
		children = []core.Node{
			core.View{Class: "gap-3", Children: []core.Node{skeletonLine("w-44"), skeletonCards(4)}},
			core.View{Class: "gap-3", Children: []core.Node{skeletonLine("w-36"), skeletonCards(4)}},
		}
	case pageSearch:
		children = []core.Node{core.View{Class: "gap-3", Children: []core.Node{skeletonLine("w-28"), skeletonCards(4)}}}
	default:
		children = []core.Node{skeletonCards(8)}
	}
	return core.View{TestID: "page-loading", Class: "flex-1 gap-8 overflow-hidden px-7 py-6", Children: children}
}

// retitle titles the page showing title when it has none.
func (sh *shell) retitle(title string) {
	if title == "" {
		return
	}
	sh.stack.Update(func(s []route) []route {
		if s[len(s)-1].title != "" {
			return s
		}
		s = append([]route(nil), s...)
		s[len(s)-1].title = title
		return s
	})
}

// section is a titled row of cards, with More for the rest of them; a
// section without a title shows its cards alone.
func (sh *shell) section(cx *core.Scope, p *plugin.Plugin, s plugin.Section) core.Node {
	var children []core.Node
	if s.Title != "" || s.More != "" {
		var more func()
		if s.More != "" {
			more = func() { sh.open(route{kind: routeBrowse, plugin: p, id: s.More, title: s.Title}) }
		}
		children = append(children, sectionHeader(cx, s.Title, "more-"+s.More, more))
	}
	var cards []core.Node
	for _, it := range s.Items {
		cards = append(cards, sh.card(cx, p, it))
	}
	children = append(children, core.View{Class: cardRow, Children: cards})
	return core.View{Class: "gap-3", Children: children}
}

// errorText is what the viewer reads of a plugin's error: the plugin's
// own message, else the app's.
func errorText(cx *core.Scope, err error) string {
	var pe *plugin.Error
	var ie *plugin.InvalidError
	switch {
	case errors.As(err, &pe):
		return pe.Message
	case errors.As(err, &ie):
		return locales.PluginInvalid(cx)
	case errors.Is(err, plugin.ErrBusy), errors.Is(err, plugin.ErrMemory), errors.Is(err, context.DeadlineExceeded):
		return locales.PluginStopped(cx)
	}
	return locales.PluginFailed(cx)
}

// failure fills a page with why a call of p failed, with what the
// viewer can do: sign in, set the plugin up, or try again.
func (sh *shell) failure(cx *core.Scope, p *plugin.Plugin, err error, retry func()) core.Node {
	msg := errorText(cx, err)
	switch {
	case errors.Is(err, plugin.ErrSignIn):
		a := sh.account(p)
		return state(stateAsk, lucide.LogIn, locales.SignInTo(cx, p.Name()), msg,
			ui.Button{TestID: "error-sign-in", Text: locales.SignIn(cx), Busy: a.busy.Get, OnPress: func() { sh.signIn(p) }})
	case errors.Is(err, plugin.ErrSetup):
		return state(stateAsk, lucide.FolderCog, locales.NeedsSetup(cx, p.Name()), msg,
			ui.Button{TestID: "error-settings", Text: locales.OpenSettings(cx), OnPress: func() { sh.openPlugin(p) }})
	case errors.Is(err, plugin.ErrUnavailable):
		return state(stateError, lucide.CircleAlert, locales.CouldNotAnswer(cx, p.Name()), msg)
	}
	return state(stateError, lucide.CircleAlert, locales.CouldNotAnswer(cx, p.Name()), msg, retryButton(cx, retry))
}

// fail tells the viewer in a toast why a call of p failed.
func (sh *shell) fail(cx *core.Scope, p *plugin.Plugin, err error) { sh.failAs(cx, p, "", err) }

// failAs tells the viewer in a toast what failed, title, and why, with
// what they can do; "" titles it with why.
func (sh *shell) failAs(cx *core.Scope, p *plugin.Plugin, title string, err error) {
	t := ui.ToastContent{ID: "error", Type: ui.ToastError, Title: errorText(cx, err)}
	if title != "" {
		t.Title, t.Description = title, t.Title
	}
	switch {
	case errors.Is(err, plugin.ErrSignIn):
		t.Action, t.OnAction = locales.SignIn(cx), func() { sh.signIn(p) }
	case errors.Is(err, plugin.ErrSetup):
		t.Action, t.OnAction = locales.OpenSettings(cx), func() { sh.openPlugin(p) }
	}
	sh.toaster.Show(t)
}

// clock writes seconds as m:ss or h:mm:ss.
func clock(seconds float64) string {
	s := int(seconds)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
