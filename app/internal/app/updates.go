package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/i18n"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/internal/store"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// checkEvery is how often the app checks the repositories of the plugins
// installed from GitHub, besides at its start.
const checkEvery = 6 * time.Hour

// update is a newer release of a plugin installed from GitHub: the
// plugin read from it, or why it cannot be installed, as a newer
// minAppVersion.
type update struct {
	id      string
	release *release
	c       *candidate
	err     error
}

// version is the update's version: its manifest's, else its tag's.
func (u *update) version() string {
	if u.c != nil {
		return u.c.manifest.Version
	}
	return strings.TrimPrefix(u.release.Tag, "v")
}

// checkState is how checking for updates goes: running, when it last
// finished and why it failed, if it did.
type checkState struct {
	busy bool
	at   time.Time
	err  error
}

// repoError is why checking one repository failed.
type repoError struct {
	repo string
	err  error
}

func (e *repoError) Error() string { return e.repo + ": " + e.err.Error() }
func (e *repoError) Unwrap() error { return e.err }

// fromGitHub are the plugins installed from GitHub with their releases.
func (sh *shell) fromGitHubPlugins() map[*plugin.Plugin]store.Release {
	found := map[*plugin.Plugin]store.Release{}
	for _, p := range sh.plugins.GetUntracked() {
		if r, ok := sh.library.releaseOf(p); ok {
			found[p] = r
		}
	}
	return found
}

// checkUpdates checks now, then every few hours.
func (sh *shell) checkUpdates() {
	sh.checkNow()
	sh.cx.AfterFunc(checkEvery, sh.checkUpdates)
}

// checkNow asks GitHub for the latest release of each plugin installed
// from it and reads those newer than the version installed, never a
// pre-release.
func (sh *shell) checkNow() {
	cx := sh.cx
	st := sh.checking.GetUntracked()
	if st.busy {
		return
	}
	installed := sh.fromGitHubPlugins()
	if len(installed) == 0 {
		return
	}
	known := sh.updates.GetUntracked()
	sh.checking.Set(checkState{busy: true, at: st.at})
	call(cx, sh.ctx, func(ctx context.Context) (map[string]*update, error) {
		found := map[string]*update{}
		var errs []error
		for p, kept := range installed {
			id := p.Manifest.ID
			r, err := sh.gitHub.latest(ctx, kept.Repo)
			if err != nil {
				errs = append(errs, &repoError{kept.Repo, err})
				if u := known[id]; u != nil {
					found[id] = u
				}
				continue
			}
			if r.Tag == kept.Tag {
				continue
			}
			if u := known[id]; u != nil && u.release.Tag == r.Tag {
				found[id] = u
				continue
			}
			c, err := sh.library.inspectRelease(ctx, sh.gitHub, r, func(string) bool { return false })
			switch {
			case errors.As(err, new(*plugin.TooNewError)):
				found[id] = &update{id: id, release: r, err: err}
			case err != nil:
				errs = append(errs, &repoError{kept.Repo, err})
			case c.manifest.ID != id:
				errs = append(errs, &repoError{kept.Repo, fmt.Errorf("its latest release holds plugin %s, not %s", c.manifest.ID, id)})
			case plugin.Newer(c.manifest.Version, p.Manifest.Version):
				found[id] = &update{id: id, release: r, c: c}
			}
		}
		var err error
		if len(errs) > 0 {
			err = errs[0]
		}
		return found, err
	}, func(found map[string]*update, err error) {
		cx.Batch(func() {
			sh.updates.Set(found)
			sh.checking.Set(checkState{at: cx.Now(), err: err})
		})
	})
}

// updateOf is the update of p, if one was found.
func (sh *shell) updateOf(p *plugin.Plugin) *update {
	return sh.updates.Get()[p.Manifest.ID]
}

// askUpdate opens the update dialog for u.
func (sh *shell) askUpdate(u *update) {
	sh.updateState.Set(installState{})
	sh.updating.Set(u)
}

// pluginByID is the plugin loaded of id, nil for none.
func (sh *shell) pluginByID(id string) *plugin.Plugin {
	for _, p := range sh.plugins.GetUntracked() {
		if p.Manifest.ID == id {
			return p
		}
	}
	return nil
}

// replacePlugin puts p in old's place: in the list, the pages open and
// what plays, whose pages load again with it.
func (sh *shell) replacePlugin(old, p *plugin.Plugin) {
	p.SetLanguage(core.Untracked(sh.cx, func() string { return i18n.Language(sh.cx) }))
	delete(sh.accounts, old)
	sh.plugins.Update(func(ps []*plugin.Plugin) []*plugin.Plugin {
		ps = slices.Clone(ps)
		for i, q := range ps {
			if q == old {
				ps[i] = p
			}
		}
		return ps
	})
	sh.stack.Update(func(s []route) []route {
		s = slices.Clone(s)
		for i, r := range s {
			if r.plugin == old {
				sh.opened++
				r.plugin, r.n = p, sh.opened
				s[i] = r
			}
		}
		return s
	})
	if sh.replaced != nil {
		sh.replaced(old, p)
	}
}

// updateDialog asks the viewer to update a plugin to its newer release,
// with the release's notes and any host it adds, then updates it, keeping
// its data.
func (sh *shell) updateDialog(cx *core.Scope) core.Node {
	cancel := func() {
		if !sh.updateState.GetUntracked().busy {
			sh.updating.Set(nil)
		}
	}
	confirm := func() {
		u := sh.updating.GetUntracked()
		if u == nil || u.c == nil || sh.updateState.GetUntracked().busy {
			return
		}
		old := sh.pluginByID(u.id)
		if old == nil {
			return
		}
		sh.updateState.Set(installState{busy: true})
		call(cx, sh.ctx, func(context.Context) (*plugin.Plugin, error) { return sh.library.update(old, u.c) }, func(p *plugin.Plugin, err error) {
			cx.Batch(func() {
				if p != nil {
					sh.replacePlugin(old, p)
				}
				if err != nil {
					sh.updateState.Set(installState{err: err})
					return
				}
				sh.updates.Update(func(m map[string]*update) map[string]*update {
					m = clonedUpdates(m)
					delete(m, u.id)
					return m
				})
				sh.updateState.Set(installState{})
				sh.updating.Set(nil)
			})
		})
	}
	return ui.Dialog{
		TestID: "update",
		Bare:   true,
		Open:   func() bool { return sh.updating.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				cancel()
			}
		},
		NoCloseOnBackdropPress: true,
		LabelFunc: func() string {
			if u := sh.updating.Get(); u != nil {
				if p := sh.pluginByID(u.id); p != nil {
					return locales.UpdatePlugin(cx, p.Name(), u.version())
				}
			}
			return ""
		},
		Style: core.Style{Width: core.Pt(480), CornerRadius: 16},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			u := sh.updating.Get()
			if u == nil {
				return nil
			}
			p := sh.pluginByID(u.id)
			if p == nil {
				return nil
			}
			return sh.updateContent(cx, p, u, confirm, cancel)
		})},
	}
}

func clonedUpdates(m map[string]*update) map[string]*update {
	out := make(map[string]*update, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// updateContent is the update dialog's content for p and its update u.
func (sh *shell) updateContent(cx *core.Scope, p *plugin.Plugin, u *update, confirm, cancel func()) core.Node {
	head := func(title, sub string) core.Node {
		return core.View{Class: "flex-row items-center gap-3.5", Children: []core.Node{
			tile(p.Name(), "size-12 rounded-2xl shadow-md", "text-xl", colorsOf(u.id)),
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.Text{TestID: "update-title", Content: title, Class: "text-[17px] font-bold text-foreground"},
				core.Text{Content: sub, MaxLines: 1, Class: "text-xs text-muted-foreground"},
			}},
		}}
	}
	have := p.Manifest.Version
	return core.Dynamic(func(cx *core.Scope) core.Node {
		st := sh.updateState.Get()
		var body []core.Node
		confirmText := locales.Update(cx)
		failed := st.err
		if failed == nil {
			failed = u.err
		}
		switch {
		case st.busy:
			body = []core.Node{
				head(locales.UpdatingPlugin(cx, p.Name(), u.version()), locales.YouHave(cx, have)),
				core.View{Class: "justify-center gap-2 py-6", Children: []core.Node{
					ui.Progress{Indeterminate: func() bool { return true }, Label: locales.UpdatingShort(cx)},
					core.Text{Content: locales.UpdatingShort(cx), Class: "text-xs text-muted-foreground"},
				}},
			}
		case failed != nil:
			confirmText = locales.Retry(cx)
			body = []core.Node{
				head(locales.CannotUpdate(cx, p.Name()), locales.StaysInstalled(cx, have)),
				core.View{TestID: "update-error", Class: "flex-row gap-2.5 rounded-xl border border-error-line bg-error-soft px-4 py-3.5", Children: []core.Node{
					core.Icon{SVG: lucide.CircleAlert(), Class: "size-4 text-error-icon"},
					core.Text{Content: installError(cx, failed), Class: "flex-1 text-[13px] leading-relaxed text-error-ink", Style: core.Style{FlexShrink: 1}},
				}},
			}
		default:
			body = []core.Node{head(locales.UpdatePlugin(cx, p.Name(), u.version()), locales.YouHaveReleased(cx, have, u.release.Published))}
			if u.release.notes != "" {
				body = append(body, core.View{TestID: "release-notes", Class: "gap-1.5 overflow-hidden rounded-xl border border-border px-4 py-3", Children: []core.Node{
					core.Text{Content: locales.WhatsNew(cx), Class: "text-xs font-semibold text-muted-foreground"},
					core.Text{Content: u.release.notes, MaxLines: 10, Class: "text-[13px] leading-relaxed text-foreground"},
				}})
			}
			added := addedHosts(p.Manifest.Permissions.Network, u.c.manifest.Permissions.Network)
			if len(added) == 0 {
				body = append(body, core.View{TestID: "same-hosts", Class: "flex-row items-center gap-2", Children: []core.Node{
					core.Icon{SVG: lucide.ShieldCheck(), Class: "size-4 text-ok-icon"},
					core.Text{Content: locales.SameHosts(cx), Class: "text-[13px] text-muted-foreground"},
				}})
			} else {
				confirmText = locales.ApproveAndUpdate(cx)
				var chips []core.Node
				for _, h := range u.c.manifest.Permissions.Network {
					if slices.Contains(added, h) {
						chips = append(chips, newHostChip(h))
					} else {
						chips = append(chips, hostChip(h))
					}
				}
				body = append(body, core.View{TestID: "new-hosts", Class: "gap-2.5 rounded-xl border border-warn-line bg-warn-soft px-4 py-3.5", Children: []core.Node{
					core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
						core.Icon{SVG: lucide.ShieldAlert(), Class: "size-4 text-warn-icon"},
						core.Text{Content: locales.AlsoConnects(cx), Class: "text-[13px] font-semibold text-foreground"},
					}},
					core.View{Class: "flex-row flex-wrap gap-1.5", Children: chips},
				}})
			}
		}
		buttons := []core.Node{ui.Button{TestID: "cancel-update", Text: locales.Cancel(cx), Variant: ui.ButtonOutline, Disabled: func() bool { return st.busy }, OnPress: cancel}}
		if failed != nil {
			buttons[0] = ui.Button{TestID: "cancel-update", Text: locales.Close(cx), Variant: ui.ButtonOutline, OnPress: cancel}
		}
		// A release that needs a newer app fails again; the others may
		// not.
		if u.c != nil {
			buttons = append(buttons, ui.Button{TestID: "confirm-update", Text: confirmText, Busy: func() bool { return st.busy }, OnPress: confirm})
		}
		return core.View{Children: []core.Node{
			core.View{Class: "gap-4 px-6 pt-6 pb-5", Children: body},
			dialogActions(nil, buttons...),
		}}
	})
}

// addedHosts are the hosts of now that before lacks.
func addedHosts(before, now []string) []string {
	var added []string
	for _, h := range now {
		if !slices.Contains(before, h) {
			added = append(added, h)
		}
	}
	return added
}

// newHostChip is a host a newer release adds.
func newHostChip(host string) core.Node {
	return core.View{TestID: "new-host-" + host, Class: "flex-row items-center gap-1.5 rounded-md border border-warn-line bg-background px-2 py-1", Children: []core.Node{
		core.Icon{SVG: lucide.Plus(), Class: "size-3 text-warn-icon"},
		core.Text{Content: host, Class: "text-xs font-semibold text-warn-ink"},
	}}
}

// updatePill is the plugin row's offer of p's update, which asks to
// install it.
func (sh *shell) updatePill(cx *core.Scope, p *plugin.Plugin) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		u := sh.updateOf(p)
		if u == nil {
			return nil
		}
		label := locales.UpdateTo(cx, u.version())
		return core.View{
			TestID: "update-" + p.Manifest.ID, Role: core.RoleButton, Label: label,
			OnPress: func(core.PressEvent) { sh.askUpdate(u) },
			Class:   "h-6 flex-row items-center gap-1 rounded-full bg-accent/10 px-2.5",
			Children: []core.Node{
				core.Icon{SVG: lucide.CircleArrowUp(), Class: "size-3.5 text-accent"},
				core.Text{Content: label, Class: "text-xs font-semibold text-accent-ink"},
			},
		}
	})
}

// updateBanner is p's page's offer of its update.
func (sh *shell) updateBanner(cx *core.Scope, p *plugin.Plugin) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		u := sh.updateOf(p)
		if u == nil {
			return nil
		}
		return core.View{TestID: "update-banner", Class: "flex-row items-center gap-3 rounded-xl bg-accent/[0.08] px-4 py-3", Children: []core.Node{
			core.Icon{SVG: lucide.CircleArrowUp(), Class: "size-5 text-accent"},
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.Text{Content: locales.VersionAvailable(cx, u.version()), Class: "text-[13px] font-semibold text-foreground"},
				core.Text{Content: locales.Released(cx, u.release.Published), Class: "text-xs text-muted-foreground"},
			}},
			ui.Button{TestID: "page-update", Text: locales.UpdateAction(cx), Variant: ui.ButtonGhost, OnPress: func() { sh.askUpdate(u) }},
		}}
	})
}

// checkStatus is the settings' word on checking for updates, with
// checking again, while plugins from GitHub are installed.
func (sh *shell) checkStatus(cx *core.Scope) core.Node {
	now := core.NewSignal(cx, cx.Now())
	var tick func()
	tick = func() {
		now.Set(cx.Now())
		cx.AfterFunc(time.Minute, tick)
	}
	cx.AfterFunc(time.Minute, tick)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		sh.plugins.Get()
		st := sh.checking.Get()
		if len(sh.fromGitHubPlugins()) == 0 {
			return nil
		}
		if st.busy {
			return core.View{TestID: "checking", Class: "flex-row items-center gap-1.5", Children: []core.Node{
				ui.Spinner{Size: 14},
				core.Text{Content: locales.CheckingUpdates(cx), Class: "text-xs text-muted-foreground"},
			}}
		}
		if st.err != nil {
			return nil
		}
		children := []core.Node{}
		if !st.at.IsZero() {
			children = append(children, core.Text{TestID: "checked", ContentFunc: func() string { return checkedAgo(cx, now.Get().Sub(st.at)) }, Class: "text-xs text-faint-ink"})
		}
		children = append(children, ui.IconButton{TestID: "check-updates", Icon: lucide.RefreshCw, Label: locales.CheckForUpdates(cx), Variant: ui.IconButtonGhost, Size: 24, IconSize: 14, OnPress: sh.checkNow})
		return core.View{Class: "flex-row items-center gap-1", Children: children}
	})
}

// checkedAgo says how long ago the last check finished.
func checkedAgo(cx *core.Scope, d time.Duration) string {
	switch {
	case d < time.Minute:
		return locales.CheckedJustNow(cx)
	case d < time.Hour:
		return locales.CheckedMinutesAgo(cx, int(d/time.Minute))
	}
	return locales.CheckedHoursAgo(cx, int(d/time.Hour))
}

// checkFailed says why the last check failed, with trying again.
func (sh *shell) checkFailed(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		st := sh.checking.Get()
		if st.busy || st.err == nil {
			return nil
		}
		reason := installError(cx, st.err)
		var re *repoError
		if errors.As(st.err, &re) {
			reason = locales.RepoReason(cx, re.repo, installError(cx, re.err))
		}
		return core.View{TestID: "check-failed", Class: "flex-row items-center gap-2.5 rounded-xl border border-warn-line bg-warn-soft px-3.5 py-2.5", Children: []core.Node{
			core.Icon{SVG: lucide.CloudOff(), Class: "size-4 text-warn-icon"},
			core.Text{Content: locales.CouldNotCheck(cx, reason), Class: "flex-1 text-[13px] text-warn-ink", Style: core.Style{FlexShrink: 1}},
			ui.Button{TestID: "check-again", Text: locales.Retry(cx), Variant: ui.ButtonGhost, Size: ui.SizeSmall, OnPress: func() { sh.checkNow() }},
		}}
	})
}
