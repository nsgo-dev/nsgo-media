package app

import (
	"context"
	"errors"
	"strconv"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// The shows the viewer follows live on a plugin's account: the app asks
// following for them, follow and unfollow to change them, and keeps
// none itself.

// followingEntry is p's sidebar entry of the shows followed, with a badge
// counting those with new episodes, asked again after signing in or out
// and after a follow changed here.
func (sh *shell) followingEntry(cx *core.Scope, p *plugin.Plugin) core.Node {
	fresh := core.NewSignal(cx, 0)
	a := sh.account(p)
	ctx := cx.Context()
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
				fresh.Set(count)
			})
		})
	})
	return badgedEntry(cx, lucide.Tv, locales.Following(cx), "following-badge-"+p.Manifest.ID, fresh.Get)
}

// followingPage shows the shows p's account follows, of the status the
// tabs choose when the plugin offers several.
func (sh *shell) followingPage(cx *core.Scope, p *plugin.Plugin) core.Node {
	status := core.NewSignal(cx, "")
	children := []core.Node{}
	if statuses := p.Manifest.Statuses; len(statuses) > 1 {
		options := []ui.Option[string]{{Value: "", Label: locales.StatusAll(cx), TestID: "status-all"}}
		for _, s := range statuses {
			options = append(options, ui.Option[string]{Value: s.ID, Label: p.Translate(s.Label), TestID: "status-" + s.ID})
		}
		children = append(children, core.View{Class: "flex-row px-7 pt-5", Children: []core.Node{ui.Tabs[string]{
			TestID:   "status",
			Label:    locales.FollowStatus(cx),
			Size:     ui.SizeSmall,
			Options:  options,
			Value:    status.Get,
			OnChange: status.Set,
		}}})
	}
	children = append(children, core.Dynamic(func(cx *core.Scope) core.Node { return sh.shows(cx, p, status.Get()) }))
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: children}
}

// shows are the shows followed of status, "" for all, with the pages
// after them on request or once scrolled to the end, asked again after
// signing in or out and after a follow changed.
func (sh *shell) shows(cx *core.Scope, p *plugin.Plugin, status string) core.Node {
	pages := core.NewSignalFunc[[]*plugin.Following](cx, nil, nil)
	loading := core.NewSignal(cx, false)
	failed := core.NewSignalFunc[error](cx, nil, nil)
	ctx := cx.Context()
	gen := 0
	fetch := func(cursor string) {
		gen++
		n := gen
		loading.Set(true)
		failed.Set(nil)
		params := plugin.FollowingParams{Status: status, Cursor: cursor}
		call(cx, ctx, func(ctx context.Context) (*plugin.Following, error) { return p.Following(ctx, params) }, func(f *plugin.Following, err error) {
			if n != gen {
				return
			}
			loading.Set(false)
			if err != nil {
				failed.Set(err)
				return
			}
			if cursor == "" {
				pages.Set([]*plugin.Following{f})
				return
			}
			pages.Update(func(ps []*plugin.Following) []*plugin.Following { return append(ps[:len(ps):len(ps)], f) })
		})
	}
	a := sh.account(p)
	cx.Effect(func() {
		a.epoch.Get()
		a.follows.Get()
		cx.Untrack(func() { fetch("") })
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
	all := func() []plugin.Show {
		var shows []plugin.Show
		for _, f := range pages.Get() {
			shows = append(shows, f.Shows...)
		}
		return shows
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		started := len(pages.Get()) > 0
		switch {
		case !started && failed.Get() != nil:
			return sh.failure(cx, p, failed.Get(), func() { fetch("") })
		case !started:
			return core.View{TestID: "page-loading", Class: "flex-1 gap-3 overflow-hidden px-7 py-6", Children: []core.Node{skeletonLine("w-36"), skeletonCards(8)}}
		case len(all()) == 0 && status == "":
			return state(stateQuiet, lucide.Tv, locales.NotFollowing(cx), locales.FollowingEmpty(cx, p.Name()))
		case len(all()) == 0:
			return state(stateQuiet, lucide.Inbox, locales.NothingHere(cx), locales.PageEmpty(cx))
		}
		return ui.ScrollView{
			TestID:       "shows",
			Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
			ContentStyle: core.Style{Padding: core.Symmetric(24, 28), Gap: 28},
			OnScroll: func(e core.ScrollEvent) {
				if e.Offset.Y+e.Size.Height >= e.ContentSize.Height-240 {
					more()
				}
			},
			Children: []core.Node{
				core.Dynamic(func(cx *core.Scope) core.Node {
					return core.View{Class: "gap-7", Children: sh.showGroups(cx, p, status, all())}
				}),
				core.Show{
					When: func() bool { return loading.Get() && len(pages.Get()) > 0 && next() != "" },
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

// showGroups lays out the shows of status: all of them with those with
// new episodes first, then by the manifest's statuses, or those of one
// status alone.
func (sh *shell) showGroups(cx *core.Scope, p *plugin.Plugin, status string, shows []plugin.Show) []core.Node {
	group := func(title, testID string, shows []plugin.Show) core.Node {
		var cards []core.Node
		for _, s := range shows {
			cards = append(cards, sh.showCard(cx, p, s))
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
	var fresh []plugin.Show
	byStatus := map[string][]plugin.Show{}
	for _, s := range shows {
		if s.New {
			fresh = append(fresh, s)
		} else {
			byStatus[s.Status] = append(byStatus[s.Status], s)
		}
	}
	if len(fresh) > 0 {
		groups = append(groups, group(locales.NewEpisodes(cx), "shows-new", fresh))
	}
	for _, st := range p.Manifest.Statuses {
		if len(byStatus[st.ID]) > 0 {
			groups = append(groups, group(p.Translate(st.Label), "shows-"+st.ID, byStatus[st.ID]))
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

// badgedEntry is a sidebar entry whose badge, testID, counts something
// while there is any.
func badgedEntry(cx *core.Scope, icon func() *core.SVG, text, testID string, count func() int) core.Node {
	label := ui.Text(text, 13, ui.Regular, ui.ColorsOf(cx).Text)
	label.MaxLines, label.Style.FlexGrow, label.Style.FlexShrink = 1, 1, 1
	return core.Group{
		ui.Icon(icon, 16, ui.ColorsOf(cx).Accent),
		label,
		core.Show{
			When: func() bool { return count() > 0 },
			Then: func(cx *core.Scope) core.Node {
				return core.View{TestID: testID, Class: "h-[18px] min-w-[18px] items-center justify-center rounded-full bg-accent px-1.5", Children: []core.Node{
					core.Text{ContentFunc: func() string { return strconv.Itoa(count()) }, Class: "text-[11px] font-semibold text-white"},
				}}
			},
		},
	}
}
