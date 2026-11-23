package app

import (
	"context"
	"log"
	"slices"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// installState is how installing the candidate goes: copying it, or why
// it failed.
type installState struct {
	busy bool
	err  error
}

// installDialog asks the viewer to approve the hosts of the plugin being
// added before any of its code runs, then installs it, showing how that
// goes.
func (sh *shell) installDialog(cx *core.Scope) core.Node {
	cancel := func() {
		if !sh.install.GetUntracked().busy {
			sh.installing.Set(nil)
		}
	}
	install := func() {
		c := sh.installing.GetUntracked()
		if c == nil || sh.install.GetUntracked().busy {
			return
		}
		sh.install.Set(installState{busy: true})
		call(cx, sh.ctx, func(context.Context) (*plugin.Plugin, error) { return sh.library.install(c) }, func(p *plugin.Plugin, err error) {
			if sh.installing.GetUntracked() != c {
				if p != nil {
					p.Close()
				}
				return
			}
			if err != nil {
				sh.install.Set(installState{err: err})
				return
			}
			if err := sh.settings.SetEnabled(p.Manifest.ID, true); err != nil {
				log.Printf("settings: %v", err)
			}
			cx.Batch(func() {
				sh.installing.Set(nil)
				sh.install.Set(installState{})
				sh.toggled.Update(func(n int) int { return n + 1 })
				sh.plugins.Update(func(ps []*plugin.Plugin) []*plugin.Plugin { return append(slices.Clip(ps), p) })
				if p.Has(plugin.ExportHome) {
					sh.choose(cx, p.Manifest.ID+"/home")
				}
			})
		})
	}
	return ui.Dialog{
		TestID: "install",
		Bare:   true,
		Open:   func() bool { return sh.installing.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				cancel()
			}
		},
		NoCloseOnEscape:        false,
		NoCloseOnBackdropPress: true,
		LabelFunc: func() string {
			if c := sh.installing.Get(); c != nil {
				return approveTitle(cx, c)
			}
			return ""
		},
		Style: core.Style{Width: core.Pt(480), CornerRadius: 16},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			c := sh.installing.Get()
			if c == nil {
				return nil
			}
			return sh.approve(cx, c, install, cancel)
		})},
	}
}

// approveTitle asks to install or use c.
func approveTitle(cx *core.Scope, c *candidate) string {
	if c.dir != "" {
		return locales.UsePlugin(cx, c.manifest.Name, c.manifest.Version)
	}
	return locales.InstallPlugin(cx, c.manifest.Name, c.manifest.Version)
}

// approve is the install dialog's content for c: what it is and the
// hosts it may reach, then how installing it goes.
func (sh *shell) approve(cx *core.Scope, c *candidate, install, cancel func()) core.Node {
	m := c.manifest
	from := c.from
	if c.dir != "" {
		from = c.dir
	}
	head := func(title string, sub core.Node) core.Node {
		return core.View{Class: "flex-row items-center gap-3.5", Children: []core.Node{
			tile(m.Name, "size-12 rounded-2xl shadow-md", "text-xl", colorsOf(m.ID)),
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.Text{TestID: "install-title", Content: title, Class: "text-[17px] font-bold text-foreground"},
				sub,
			}},
		}}
	}
	icon := lucide.Folder
	if c.release != nil {
		icon = lucide.GitFork
	}
	where := func(text string) core.Node {
		if text == "" {
			return core.View{}
		}
		return core.View{Class: "flex-row items-center gap-1", Children: []core.Node{
			core.Icon{SVG: icon(), Class: "size-3 text-faint-ink"},
			core.Text{Content: text, MaxLines: 1, Class: "text-xs text-muted-foreground", Style: core.Style{FlexShrink: 1}},
		}}
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		st := sh.install.Get()
		var body []core.Node
		confirmText := locales.Install(cx)
		if c.dir != "" {
			confirmText = locales.Use(cx)
		}
		switch {
		case st.busy:
			body = []core.Node{
				head(locales.Installing(cx, m.Name, m.Version), where(from)),
				core.View{Class: "flex-1 justify-center gap-2 py-6", Children: []core.Node{
					ui.Progress{Indeterminate: func() bool { return true }, Label: locales.InstallingShort(cx)},
					core.Text{Content: locales.InstallingShort(cx), Class: "text-xs text-muted-foreground"},
				}},
			}
		case st.err != nil:
			confirmText = locales.Retry(cx)
			sub := m.Name + " " + m.Version
			if from != "" {
				sub += " · " + from
			}
			body = []core.Node{
				head(locales.CannotInstall(cx), core.Text{Content: sub, MaxLines: 1, Class: "text-xs text-muted-foreground"}),
				core.View{TestID: "install-error", Class: "flex-row gap-2.5 rounded-xl border border-error-line bg-error-soft px-4 py-3.5", Children: []core.Node{
					core.Icon{SVG: lucide.CircleAlert(), Class: "size-4 text-error-icon"},
					core.Text{Content: installError(cx, st.err), Class: "flex-1 text-[13px] leading-relaxed text-error-ink", Style: core.Style{FlexShrink: 1}},
				}},
			}
		default:
			sub := from
			if c.release != nil {
				sub = locales.RepoReleased(cx, from, c.release.Published)
			}
			body = []core.Node{head(approveTitle(cx, c), where(sub)), hostsBox(cx, m.Permissions.Network)}
		}
		return core.View{Children: []core.Node{
			core.View{Class: "gap-4 px-6 pt-6 pb-5", Children: body},
			dialogActions(nil,
				ui.Button{TestID: "cancel-install", Text: locales.Cancel(cx), Variant: ui.ButtonOutline, Disabled: func() bool { return st.busy }, OnPress: cancel},
				ui.Button{TestID: "confirm-install", Text: confirmText, Busy: func() bool { return st.busy }, OnPress: install},
			),
		}}
	})
}

// hostsBox lists the hosts a plugin being added may reach, and that it
// reaches nothing else.
func hostsBox(cx *core.Scope, hosts []string) core.Node {
	if len(hosts) == 0 {
		return core.View{Class: "gap-2.5 rounded-xl border border-border bg-chip/60 px-4 py-3.5", Children: []core.Node{
			core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
				core.Icon{SVG: lucide.WifiOff(), Class: "size-4 text-muted-foreground"},
				core.Text{Content: locales.ReachesNoHosts(cx), Class: "text-[13px] font-semibold text-foreground"},
			}},
			core.Text{Content: locales.ReachesNothingElse(cx), Class: "text-xs leading-relaxed text-muted-foreground"},
		}}
	}
	var chips []core.Node
	for _, h := range hosts {
		chips = append(chips, hostChip(h))
	}
	return core.View{Class: "gap-2.5 rounded-xl border border-border bg-chip/60 px-4 py-3.5", Children: []core.Node{
		core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
			core.Icon{SVG: lucide.ShieldCheck(), Class: "size-4 text-ok-icon"},
			core.Text{Content: locales.ReachesHosts(cx), Class: "text-[13px] font-semibold text-foreground"},
		}},
		core.View{Class: "flex-row flex-wrap gap-1.5", Children: chips},
		core.Text{Content: locales.ReachesNoOtherHost(cx), Class: "text-xs leading-relaxed text-muted-foreground"},
	}}
}
