package app

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// The shows the viewer follows live on a plugin's account: the app asks
// following for them, follow and unfollow to change them, and keeps
// none itself.

// newsWatcher counts the shows with new episodes of every enabled plugin
// following, for the toolbar's badge, asked again after signing in or
// out and after a follow changed here. It shows nothing.
func (sh *shell) newsWatcher(cx *core.Scope) core.Node {
	ctx := cx.Context()
	return core.Each(func() []*plugin.Plugin { return sh.sources(plugin.ExportFollowing) }, func(p *plugin.Plugin) *plugin.Plugin { return p }, func(cx *core.Scope, item func() *plugin.Plugin, _ func() int) core.Node {
		p := item()
		a := sh.account(p)
		gen := 0
		cx.Effect(func() {
			a.epoch.Get()
			a.follows.Get()
			cx.Untrack(func() {
				gen++
				n := gen
				call(cx, ctx, func(ctx context.Context) (*plugin.Following, error) {
					return p.Following(ctx, plugin.FollowingParams{})
				}, func(f *plugin.Following, err error) {
					if n != gen {
						return
					}
					count := 0
					if err == nil {
						for _, s := range f.Shows {
							if s.New {
								count++
							}
						}
					}
					sh.setNews(p, count)
				})
			})
		})
		cx.Cleanup(func() { sh.setNews(p, 0) })
		return nil
	})
}

func (sh *shell) setNews(p *plugin.Plugin, n int) {
	sh.news.Update(func(m map[*plugin.Plugin]int) map[*plugin.Plugin]int {
		m = maps.Clone(m)
		if m == nil {
			m = map[*plugin.Plugin]int{}
		}
		if n == 0 {
			delete(m, p)
		} else {
			m[p] = n
		}
		return m
	})
}

// fresh is how many followed shows have new episodes, of the enabled
// plugins.
func (sh *shell) fresh() int {
	n := 0
	for _, p := range sh.active() {
		n += sh.news.Get()[p]
	}
	return n
}

// statusOf is a status of the plugins', by its id, and the plugin
// naming it first.
type statusOf struct {
	plugin.Status
	p *plugin.Plugin
}

// statuses are the statuses of the plugins' follows, each once.
func statuses(ps []*plugin.Plugin) []statusOf {
	var out []statusOf
	for _, p := range ps {
		for _, s := range p.Manifest.Statuses {
			if !slices.ContainsFunc(out, func(o statusOf) bool { return o.ID == s.ID }) {
				out = append(out, statusOf{s, p})
			}
		}
	}
	return out
}

// followingPage shows the shows the accounts of the enabled plugins
// follow, newest update first, or those of the plugin the filter chose,
// of the status the tabs choose when there are several.
func (sh *shell) followingPage(cx *core.Scope) core.Node {
	sources := func() []*plugin.Plugin { return sh.sources(plugin.ExportFollowing) }
	status := core.NewSignal(cx, "")
	tabs := core.Dynamic(func(cx *core.Scope) core.Node {
		all := statuses(sh.shown(sources()))
		if len(all) < 2 {
			return nil
		}
		options := []ui.Option[string]{{Value: "", Label: locales.StatusAll(cx), TestID: "status-all"}}
		for _, s := range all {
			options = append(options, ui.Option[string]{Value: s.ID, Label: s.p.Translate(s.Label), TestID: "status-" + s.ID})
		}
		return ui.Tabs[string]{
			TestID:   "status",
			Label:    locales.FollowStatus(cx),
			Size:     ui.SizeSmall,
			Options:  options,
			Value:    status.Get,
			OnChange: status.Set,
		}
	})
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{
		sh.filterBar(cx, sources, tabs),
		core.Dynamic(func(cx *core.Scope) core.Node {
			shown := sh.shown(sources())
			st := status.Get()
			if !slices.ContainsFunc(statuses(shown), func(s statusOf) bool { return s.ID == st }) {
				st = ""
			}
			return sh.shows(cx, shown, st)
		}),
	}}
}

// followed is a show of a plugin's account.
type followed struct {
	p *plugin.Plugin
	s plugin.Show
}

// shows are the shows the plugins follow of status, "" for all, with the
// pages after them on request or once scrolled to the end, asked again
// after signing in or out and after a follow changed.
func (sh *shell) shows(cx *core.Scope, plugins []*plugin.Plugin, status string) core.Node {
	var ps pagers[*plugin.Following]
	for _, p := range plugins {
		if _, ok := p.Manifest.Status(status); status != "" && !ok {
			continue
		}
		ps = append(ps, newPager(cx, p, func(ctx context.Context, cursor string) (*plugin.Following, error) {
			return p.Following(ctx, plugin.FollowingParams{Status: status, Cursor: cursor})
		}, func(f *plugin.Following) string { return f.Next }))
	}
	ps.start(cx, sh, true)
	all := func() []followed {
		var shows []followed
		for _, pg := range ps {
			for _, f := range pg.pages.Get() {
				for _, s := range f.Shows {
					shows = append(shows, followed{pg.p, s})
				}
			}
		}
		return shows
	}
	loading := core.View{TestID: "page-loading", Class: "flex-1 gap-3 overflow-hidden px-7 py-6", Children: []core.Node{skeletonLine("w-36"), skeletonCards(8)}}
	nothing := func(cx *core.Scope) core.Node {
		switch {
		case status != "":
			return state(stateQuiet, lucide.Inbox, locales.NothingHere(cx), locales.PageEmpty(cx))
		case len(plugins) == 1:
			return state(stateQuiet, lucide.Tv, locales.NotFollowing(cx), locales.FollowingEmpty(cx, plugins[0].Name()))
		}
		return state(stateQuiet, lucide.Tv, locales.NotFollowing(cx), locales.FollowingEmptyAll(cx))
	}
	return feed(cx, sh, ps, loading, func() bool { return len(all()) == 0 }, nothing, func(cx *core.Scope) core.Node {
		return core.View{Class: "gap-7", Children: sh.showGroups(cx, plugins, status, all())}
	})
}

// showGroups lays out the shows of status: all of them with those with
// new episodes first, then by the statuses, or those of one status
// alone.
func (sh *shell) showGroups(cx *core.Scope, plugins []*plugin.Plugin, status string, shows []followed) []core.Node {
	group := func(title, testID string, shows []followed) core.Node {
		var cards []core.Node
		for _, f := range shows {
			cards = append(cards, sh.showCard(cx, f.p, f.s))
		}
		children := []core.Node{}
		if title != "" {
			children = append(children, sectionHeader(cx, title, "", nil))
		}
		children = append(children, core.View{Class: cardRow, Children: cards})
		return core.View{TestID: testID, Class: "gap-3", Children: children}
	}
	if status != "" {
		return []core.Node{group("", "shows-"+status, shows)}
	}
	var groups []core.Node
	var fresh []followed
	byStatus := map[string][]followed{}
	for _, f := range shows {
		if f.s.New {
			fresh = append(fresh, f)
		} else {
			byStatus[f.s.Status] = append(byStatus[f.s.Status], f)
		}
	}
	if len(fresh) > 0 {
		groups = append(groups, group(locales.NewEpisodes(cx), "shows-new", fresh))
	}
	for _, st := range statuses(plugins) {
		if len(byStatus[st.ID]) > 0 {
			groups = append(groups, group(st.p.Translate(st.Label), "shows-"+st.ID, byStatus[st.ID]))
		}
	}
	return groups
}

// showCard is a followed show, which opens its video: its cover, marked
// when it has a new episode, how far the viewer watched and the latest
// episode.
func (sh *shell) showCard(cx *core.Scope, p *plugin.Plugin, s plugin.Show) core.Node {
	open := func(core.PressEvent) { sh.open(route{kind: routeVideo, plugin: p, id: s.Video, title: s.Title}) }
	thumb := []core.Node{sh.cover(cx, s.Image, "size-full")}
	if s.New {
		thumb = append(thumb, core.Text{TestID: "new", Content: locales.NewBadge(cx), Class: "absolute top-1.5 left-1.5 rounded-md bg-accent px-1.5 py-0.5 text-[11px] font-semibold text-white"})
	}
	thumb = append(thumb, core.View{Class: "absolute top-1.5 right-1.5", Children: []core.Node{sh.badge(p, badgeCover)}})
	if s.Episodes > 0 {
		thumb = append(thumb, bar("h-[3px]", func() float64 { return float64(s.Watched) / float64(s.Episodes) }))
	}
	watched := locales.NotStarted(cx)
	if s.Watched > 0 {
		watched = locales.WatchedEpisode(cx, s.Watched)
	}
	line := []core.Node{core.Text{Content: watched, MaxLines: 1, Class: "text-xs text-muted-foreground"}}
	if s.Episodes > 0 {
		latest, class := locales.EpisodesCount(cx, s.Episodes), "text-xs text-muted-foreground"
		if s.New {
			latest, class = locales.UpdatedTo(cx, s.Episodes), "text-xs font-medium text-accent-ink"
		}
		line = append(line,
			core.Text{Content: "·", Class: "text-xs text-faint-ink"},
			core.Text{Content: latest, MaxLines: 1, Class: class},
		)
	}
	return core.View{TestID: "show-" + s.ID, Role: core.RoleButton, Label: s.Title, OnPress: open, Class: cardWidth + " gap-2", Children: []core.Node{
		core.View{Class: "h-[120px] overflow-hidden rounded-xl bg-well shadow-sm", Children: thumb},
		core.View{Class: "gap-0.5 px-0.5", Children: []core.Node{
			core.Text{Content: s.Title, MaxLines: 1, Class: "text-[13px] leading-snug font-medium text-foreground"},
			core.View{Class: "flex-row items-center gap-1.5", Children: line},
		}},
	}}
}

// nextEpisode is the index of the episode of v's show after the last the
// viewer watched, or -1 when there is none to go on with, as when the
// plugin names the part to start with.
func nextEpisode(v *plugin.Video) int {
	if v.Show == nil || v.Part != "" || v.Show.Watched <= 0 || v.Show.Watched >= len(v.Parts) {
		return -1
	}
	return v.Show.Watched
}

// followButton follows v's show on p's account, or, once followed, shows
// its status, whose menu changes it or unfollows. It shows the change at
// once; one that fails goes back and says why.
func (sh *shell) followButton(cx *core.Scope, p *plugin.Plugin, show *plugin.VideoShow) core.Node {
	status := core.NewSignal(cx, show.Status)
	busy := core.NewSignal(cx, false)
	a := sh.account(p)
	set := func(to string) {
		was := status.GetUntracked()
		if busy.GetUntracked() || to == was {
			return
		}
		status.Set(to)
		busy.Set(true)
		call(cx, sh.ctx, func(ctx context.Context) (struct{}, error) {
			if to == "" {
				return struct{}{}, p.Unfollow(ctx, plugin.UnfollowParams{ID: show.ID})
			}
			return struct{}{}, p.Follow(ctx, plugin.FollowParams{ID: show.ID, Status: to})
		}, func(_ struct{}, err error) {
			busy.Set(false)
			if err != nil {
				status.Set(was)
				title := locales.CouldNotChangeStatus(cx)
				switch {
				case was == "":
					title = locales.CouldNotFollow(cx)
				case to == "":
					title = locales.CouldNotUnfollow(cx)
				}
				if !errors.Is(err, context.Canceled) {
					sh.failAs(cx, p, title, err)
				}
				return
			}
			a.follows.Update(func(n int) int { return n + 1 })
		})
	}
	statuses := p.Manifest.Statuses
	return core.Dynamic(func(cx *core.Scope) core.Node {
		cur := status.Get()
		st, ok := p.Manifest.Status(cur)
		if !ok {
			return ui.Button{TestID: "follow", Text: locales.Follow(cx), Icon: lucide.Plus, Variant: ui.ButtonOutline, Size: ui.SizeLarge,
				Busy: busy.Get, OnPress: func() { set(statuses[0].ID) }}
		}
		var items []core.Node
		for _, s := range statuses {
			items = append(items, ui.MenuItem{TestID: "status-" + s.ID, Text: p.Translate(s.Label),
				Checked: func() bool { return status.Get() == s.ID }, OnSelect: func() { set(s.ID) }})
		}
		if p.Has(plugin.ExportUnfollow) {
			items = append(items, ui.MenuSeparator{},
				ui.MenuItem{TestID: "unfollow", Text: locales.Unfollow(cx), Icon: lucide.X, Variant: ui.MenuItemDanger, OnSelect: func() { set("") }})
		}
		label := p.Translate(st.Label)
		return ui.Menu{
			TestID:   "follow-menu",
			Label:    label,
			Trigger:  ui.Button{Text: label, Icon: lucide.Check, Variant: ui.ButtonOutline, Size: ui.SizeLarge, Busy: busy.Get},
			Children: items,
		}
	})
}

// episodeLabels are the number and the label of v's part at index i: an
// episode's when v is a show's.
func episodeLabels(cx *core.Scope, v *plugin.Video, i int) (number, label string) {
	title := v.Parts[i].Title
	if v.Show != nil {
		return locales.EpisodeNumber(cx, i+1), locales.EpisodeLabel(cx, i+1, title)
	}
	return locales.PartNumber(cx, i+1), locales.PartLabel(cx, i+1, title)
}
