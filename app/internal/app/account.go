package app

import (
	"context"
	"errors"

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
	a := &account{who: core.NewSignal[*plugin.Account](cx, nil), busy: core.NewSignal(cx, false), epoch: core.NewSignal(cx, 0)}
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

// accountMenu ends p's sidebar title: signing in, or the account and
// signing out.
func (sh *shell) accountMenu(cx *core.Scope, p *plugin.Plugin) core.Node {
	if !p.Has(plugin.ExportSignIn) && !p.Has(plugin.ExportAccount) {
		return nil
	}
	a := sh.account(p)
	id := p.Manifest.ID
	return core.Dynamic(func(cx *core.Scope) core.Node {
		acc := a.who.Get()
		if acc == nil {
			if !p.Has(plugin.ExportSignIn) {
				return nil
			}
			return ui.IconButton{
				TestID: "sign-in-" + id, Icon: lucide.LogIn, Label: locales.SignIn(cx), Variant: ui.IconButtonGhost, Size: 24, IconSize: 14,
				Disabled: a.busy.Get, OnPress: func() { sh.signIn(p) },
			}
		}
		items := []core.Node{ui.MenuLabel{Text: acc.Name}}
		if p.Has(plugin.ExportSignOut) {
			items = append(items, ui.MenuItem{TestID: "sign-out-" + id, Text: locales.SignOut(cx), Icon: lucide.LogOut, OnSelect: func() { sh.signOut(p) }})
		}
		return ui.Menu{
			TestID:   "account-" + id,
			Label:    acc.Name,
			Trigger:  ui.Button{Icon: lucide.CircleUser, Label: acc.Name, Variant: ui.ButtonGhost, Size: ui.SizeSmall},
			Children: items,
		}
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
// and what to do.
func (sh *shell) signInDialog(cx *core.Scope) core.Node {
	ps := sh.prompts
	return ui.Dialog{
		TestID: "sign-in",
		Open:   func() bool { return ps.shown.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				ps.dismiss()
			}
		},
		TitleFunc: func() string {
			if cur := ps.shown.Get(); cur != nil {
				return locales.SignInTo(cx, cur.p.Name())
			}
			return ""
		},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			cur := ps.shown.Get()
			if cur == nil {
				return nil
			}
			var nodes []core.Node
			if cur.QRCode != "" {
				nodes = append(nodes, qrCode(cx, cur.QRCode))
			}
			if cur.Message != "" {
				nodes = append(nodes, core.Text{TestID: "sign-in-message", Content: cur.Message, Style: core.Style{TextAlign: core.TextAlignCenter}})
			}
			return core.View{Style: core.Style{AlignItems: core.AlignCenter, Gap: 12}, Children: nodes}
		})},
		Footer: []core.Node{
			ui.Button{TestID: "cancel-sign-in", Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, OnPress: ps.dismiss},
		},
	}
}
