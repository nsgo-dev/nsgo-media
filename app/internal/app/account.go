package app

import (
	"context"
	"errors"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// account is the viewer's account of a plugin.
type account struct {
	who  *core.Signal[*plugin.Account]
	busy *core.Signal[bool]
	// epoch counts signing in and out, after which pages load again.
	epoch *core.Signal[int]
	// follows counts the follows changed here, after which the shows
	// followed load again.
	follows *core.Signal[int]
}

// account is the viewer's account of p, asked of the plugin the first
// time, as the plugin runs only once enabled.
func (sh *shell) account(p *plugin.Plugin) *account {
	if a := sh.accounts[p]; a != nil {
		return a
	}
	a := sh.newAccount(sh.cx, p)
	sh.accounts[p] = a
	return a
}

func (sh *shell) newAccount(cx *core.Scope, p *plugin.Plugin) *account {
	a := &account{who: core.NewSignal[*plugin.Account](cx, nil), busy: core.NewSignal(cx, false), epoch: core.NewSignal(cx, 0), follows: core.NewSignal(cx, 0)}
	if p.Has(plugin.ExportAccount) {
		call(cx, sh.ctx, p.Account, func(acc *plugin.Account, err error) {
			if err == nil {
				a.who.Set(acc)
			}
		})
	}
	return a
}

// signIn signs the viewer in to p, through the plugin's prompt.
func (sh *shell) signIn(p *plugin.Plugin) {
	a := sh.account(p)
	if a.busy.GetUntracked() || !p.Has(plugin.ExportSignIn) {
		return
	}
	a.busy.Set(true)
	call(sh.cx, sh.ctx, p.SignIn, func(acc *plugin.Account, err error) {
		a.busy.Set(false)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				sh.fail(sh.cx, p, err)
			}
			return
		}
		a.who.Set(acc)
		a.epoch.Update(func(n int) int { return n + 1 })
	})
}

func (sh *shell) signOut(p *plugin.Plugin) {
	a := sh.account(p)
	call(sh.cx, sh.ctx, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.SignOut(ctx) }, func(_ struct{}, err error) {
		if err != nil {
			sh.fail(sh.cx, p, err)
			return
		}
		a.who.Set(nil)
		a.epoch.Update(func(n int) int { return n + 1 })
	})
}

// avatar is the account's picture in a circle of size, its initial
// while there is none.
func (sh *shell) avatar(cx *core.Scope, acc *plugin.Account, size string) core.Node {
	initial := ""
	if r := []rune(acc.Name); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	children := []core.Node{core.Text{Content: initial, Class: "text-[10px] font-semibold text-muted-foreground"}}
	if acc.Avatar != nil {
		children = append(children, sh.cover(cx, acc.Avatar, "absolute inset-0 rounded-full"))
	}
	return core.View{Class: "items-center justify-center overflow-hidden rounded-full bg-well " + size, Children: children}
}

// accountRow is who is signed in to p, with Sign Out, or else Sign In.
func (sh *shell) accountRow(cx *core.Scope, p *plugin.Plugin) core.Node {
	a := sh.account(p)
	id := p.Manifest.ID
	return core.Dynamic(func(cx *core.Scope) core.Node {
		acc := a.who.Get()
		var lead core.Node
		var name, status string
		var action core.Node
		if acc != nil {
			lead, name, status = sh.avatar(cx, acc, "size-8"), acc.Name, locales.SignedIn(cx)
			if p.Has(plugin.ExportSignOut) {
				action = ui.Button{TestID: "page-sign-out-" + id, Text: locales.SignOut(cx), Variant: ui.ButtonOutline, OnPress: func() { sh.signOut(p) }}
			}
		} else {
			lead = core.View{Class: "size-8 items-center justify-center rounded-full bg-chip", Children: []core.Node{
				core.Icon{SVG: lucide.User(), Class: "size-4 text-faint-ink"},
			}}
			name, status = locales.NotSignedIn(cx), ""
			if p.Has(plugin.ExportSignIn) {
				action = ui.Button{TestID: "page-sign-in-" + id, Text: locales.SignIn(cx), Busy: a.busy.Get, OnPress: func() { sh.signIn(p) }}
			}
		}
		text := []core.Node{core.Text{Content: name, Class: "text-[13px] font-medium text-foreground"}}
		if status != "" {
			text = append(text, core.Text{Content: status, Class: "text-xs text-muted-foreground"})
		}
		children := []core.Node{lead, core.View{Class: "flex-1", Style: core.Style{FlexShrink: 1}, Children: text}}
		if action != nil {
			children = append(children, action)
		}
		return core.View{TestID: "account-row", Class: "min-h-[52px] flex-row items-center gap-3 px-4 py-2.5", Children: children}
	})
}

// prompt is a plugin's sign-in prompt.
type prompt struct {
	p *plugin.Plugin
	plugin.SignInPrompt
}

// prompts shows plugins' sign-in prompts and opens their pages; its
// methods run on plugins' goroutines and post to the engine.
type prompts struct {
	cx    *core.Scope
	shown *core.Signal[*prompt]
}

func (ps *prompts) ShowSignIn(p *plugin.Plugin, sp plugin.SignInPrompt) {
	ps.cx.Post(func() { ps.shown.Set(&prompt{p: p, SignInPrompt: sp}) })
}

func (ps *prompts) CloseSignIn(p *plugin.Plugin) {
	ps.cx.Post(func() {
		if cur := ps.shown.Get(); cur != nil && cur.p == p {
			ps.shown.Set(nil)
		}
	})
}

func (ps *prompts) OpenURL(url string) error {
	ps.cx.Post(func() { ps.cx.OpenURL(url, func(bool) {}) })
	return nil
}

// dismiss closes the prompt showing as the viewer did, telling its
// plugin.
func (ps *prompts) dismiss() {
	cur := ps.shown.Get()
	ps.shown.Set(nil)
	if cur != nil && cur.Dismissed != nil {
		go cur.Dismissed()
	}
}

// signInDialog shows the sign-in prompt of a plugin: its QR code to scan
// and what to do, or only what to do, as when it opened its page in the
// browser.
func (sh *shell) signInDialog(cx *core.Scope) core.Node {
	ps := sh.prompts
	return ui.Dialog{
		TestID: "sign-in",
		Bare:   true,
		Open:   func() bool { return ps.shown.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				ps.dismiss()
			}
		},
		LabelFunc: func() string {
			if cur := ps.shown.Get(); cur != nil {
				return locales.SignInTo(cx, cur.p.Name())
			}
			return ""
		},
		Style: core.Style{Width: core.Pt(400), CornerRadius: 16},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			cur := ps.shown.Get()
			if cur == nil {
				return nil
			}
			title := core.Text{Content: locales.SignInTo(cx, cur.p.Name()), Class: "text-center text-[17px] font-bold text-foreground"}
			var nodes []core.Node
			if cur.QRCode != "" {
				nodes = append(nodes, title, core.View{Class: "rounded-2xl border border-border bg-white p-3 shadow-sm", Children: []core.Node{qrCode(cx, cur.QRCode)}})
			} else {
				nodes = append(nodes, core.View{Class: "size-12 items-center justify-center rounded-2xl bg-accent/10", Children: []core.Node{
					core.Icon{SVG: lucide.Globe(), Class: "size-6 text-accent"},
				}}, title)
			}
			if cur.Message != "" {
				nodes = append(nodes, core.Text{TestID: "sign-in-message", Content: cur.Message, Class: "max-w-[300px] text-center text-[13px] leading-relaxed text-muted-foreground"})
			}
			if cur.QRCode != "" {
				nodes = append(nodes, core.View{Class: "flex-row items-center gap-1.5", Children: []core.Node{
					ui.Spinner{Size: 12},
					core.Text{Content: locales.WaitingToScan(cx), Class: "text-xs text-faint-ink"},
				}})
			}
			return core.View{Children: []core.Node{
				core.View{Class: "items-center gap-4 px-6 pt-7 pb-5", Children: nodes},
				dialogActions(nil, ui.Button{TestID: "cancel-sign-in", Text: locales.Cancel(cx), Variant: ui.ButtonOutline, OnPress: ps.dismiss}),
			}}
		})},
	}
}
