package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
	"github.com/nsgo-dev/nsgo-media/app/locales"
)

// pageKind is what a page of sections is, for its loading and empty
// states.
type pageKind int

const (
	pageHome pageKind = iota
	pageBrowse
	pageSearch
)

// sources are the enabled plugins that export export.
func (sh *shell) sources(export string) []*plugin.Plugin {
	var ps []*plugin.Plugin
	for _, p := range sh.active() {
		if p.Has(export) {
			ps = append(ps, p)
		}
	}
	return ps
}

// homePage is the home pages of every enabled plugin together, or of the
// one the filter chose, after the videos the viewer may continue.
func (sh *shell) homePage(cx *core.Scope) core.Node {
	sources := func() []*plugin.Plugin { return sh.sources(plugin.ExportHome) }
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{
		sh.filterBar(cx, sources),
		core.Dynamic(func(cx *core.Scope) core.Node {
			shown := sh.shown(sources())
			return sh.sectionsPage(cx, shown, pageHome, func(p *plugin.Plugin, ctx context.Context, cursor string) (*plugin.Page, error) {
				return p.Home(ctx, plugin.PageParams{Cursor: cursor})
			}, func(cx *core.Scope) []section { return sh.continuing(cx, shown) }, func(cx *core.Scope) core.Node {
				msg := locales.HomeEmptyAll(cx)
				if len(shown) == 1 {
					msg = locales.HomeEmpty(cx, shown[0].Name())
				}
				return state(stateQuiet, lucide.Inbox, locales.NothingHere(cx), msg)
			})
		}),
	}}
}

// maxContinuing bounds the videos the home page offers to continue.
const maxContinuing = 10

// continuing is the videos of plugins played last that were left
// partway, as a section, while the plugins they came from show.
func (sh *shell) continuing(cx *core.Scope, shown []*plugin.Plugin) []section {
	sh.played.Get()
	if sh.recent == nil {
		return nil
	}
	byID := map[string]*plugin.Plugin{}
	for _, p := range shown {
		byID[p.Manifest.ID] = p
	}
	s := section{title: locales.ContinueWatching(cx)}
	for _, r := range sh.recent.List() {
		p := byID[r.Plugin]
		if p == nil || len(s.items) == maxContinuing {
			continue
		}
		if _, _, ok := sh.positions.Latest(videoKey(p, r.Video)); !ok {
			continue
		}
		var it plugin.Item
		if json.Unmarshal(r.Item, &it) == nil && it.ID == r.Video {
			s.items = append(s.items, entry{p: p, it: it})
		}
	}
	if len(s.items) == 0 {
		return nil
	}
	return []section{s}
}

// discoverPage is the first page of each enabled plugin's own together,
// or, of the one the filter chose, the page its tabs choose.
func (sh *shell) discoverPage(cx *core.Scope) core.Node {
	sources := func() []*plugin.Plugin {
		var ps []*plugin.Plugin
		for _, p := range sh.sources(plugin.ExportBrowse) {
			if len(p.Manifest.Navigation) > 0 {
				ps = append(ps, p)
			}
		}
		return ps
	}
	entry := core.NewSignal(cx, "")
	tabs := core.Dynamic(func(cx *core.Scope) core.Node {
		shown := sh.shown(sources())
		if len(shown) != 1 || len(shown[0].Manifest.Navigation) < 2 {
			return nil
		}
		p := shown[0]
		var options []ui.Option[string]
		for _, e := range p.Manifest.Navigation {
			options = append(options, ui.Option[string]{Value: e.ID, Label: p.Translate(e.Label), TestID: "entry-" + e.ID})
		}
		return ui.Tabs[string]{
			TestID: "entries", Label: locales.Discover(cx), Size: ui.SizeSmall, Options: options,
			Value: func() string {
				if id := entry.Get(); slices.ContainsFunc(p.Manifest.Navigation, func(e plugin.Entry) bool { return e.ID == id }) {
					return id
				}
				return p.Manifest.Navigation[0].ID
			},
			OnChange: entry.Set,
		}
	})
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{
		sh.filterBar(cx, sources, tabs),
		core.Dynamic(func(cx *core.Scope) core.Node {
			shown := sh.shown(sources())
			chosen := entry.Get()
			return sh.sectionsPage(cx, shown, pageBrowse, func(p *plugin.Plugin, ctx context.Context, cursor string) (*plugin.Page, error) {
				id := p.Manifest.Navigation[0].ID
				if len(shown) == 1 && slices.ContainsFunc(p.Manifest.Navigation, func(e plugin.Entry) bool { return e.ID == chosen }) {
					id = chosen
				}
				return p.Browse(ctx, plugin.BrowseParams{ID: id, Cursor: cursor})
			}, nil, func(cx *core.Scope) core.Node {
				return state(stateQuiet, lucide.Compass, locales.NothingHere(cx), locales.DiscoverEmpty(cx))
			})
		}),
	}}
}

// browsePage is a page of p's own, opened by its id.
func (sh *shell) browsePage(cx *core.Scope, p *plugin.Plugin, id string) core.Node {
	return sh.sectionsPage(cx, []*plugin.Plugin{p}, pageBrowse, func(p *plugin.Plugin, ctx context.Context, cursor string) (*plugin.Page, error) {
		return p.Browse(ctx, plugin.BrowseParams{ID: id, Cursor: cursor})
	}, nil, func(cx *core.Scope) core.Node {
		return state(stateQuiet, lucide.Inbox, locales.NothingHere(cx), locales.PageEmpty(cx))
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
