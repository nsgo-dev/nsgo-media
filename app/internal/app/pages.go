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

const cardWidth = 200

// sectionsPage shows the sections load answers, with the pages after it
// on request, again after the viewer signs in or out.
func (sh *shell) sectionsPage(cx *core.Scope, p *plugin.Plugin, load loader) core.Node {
	pages := core.NewSignalFunc[[]*plugin.Page](cx, nil, nil)
	loading := core.NewSignal(cx, false)
	failed := core.NewSignal[error](cx, nil)
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
			if len(pages.GetUntracked()) == 0 {
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
	return ui.ScrollView{
		TestID:       "sections",
		Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
		ContentStyle: core.Style{Padding: core.All(16), Gap: 24},
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
				return core.View{Style: core.Style{Gap: 24}, Children: nodes}
			}),
			core.Show{
				When: func() bool { return failed.Get() != nil },
				Then: func(cx *core.Scope) core.Node {
					return sh.errorView(cx, p, failed.GetUntracked(), func() { fetch(next()) })
				},
			},
			core.Show{
				When: loading.Get,
				Then: func(*core.Scope) core.Node {
					return core.View{Style: core.Style{AlignItems: core.AlignCenter, Padding: core.All(24)}, Children: []core.Node{ui.Spinner{Size: 24}}}
				},
			},
			core.Show{
				When: func() bool { return !loading.Get() && failed.Get() == nil && empty() },
				Then: func(cx *core.Scope) core.Node { return message(cx, locales.NothingHere(cx)) },
			},
			core.Show{
				When: func() bool { return !loading.Get() && failed.Get() == nil && next() != "" },
				Then: func(cx *core.Scope) core.Node {
					return core.View{Style: core.Style{AlignItems: core.AlignCenter}, Children: []core.Node{
						ui.Button{TestID: "load-more", Text: locales.LoadMore(cx), Variant: ui.ButtonSecondary, OnPress: func() { fetch(next()) }},
					}}
				},
			},
		},
	}
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

// section is a titled row of cards, with a link to the rest of them.
func (sh *shell) section(cx *core.Scope, p *plugin.Plugin, s plugin.Section) core.Node {
	head := []core.Node{core.Text{Content: s.Title, Style: ui.TypeTitle.Merge(core.Style{FlexShrink: 1})}}
	if s.More != "" {
		head = append(head, ui.Spacer{}, ui.Button{
			TestID:  "more-" + s.More,
			Text:    locales.More(cx),
			Variant: ui.ButtonGhost,
			OnPress: func() { sh.open(route{kind: routeBrowse, plugin: p, id: s.More, title: s.Title}) },
		})
	}
	var cards []core.Node
	for _, it := range s.Items {
		cards = append(cards, sh.card(cx, p, it))
	}
	children := []core.Node{}
	if s.Title != "" || s.More != "" {
		children = append(children, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: head})
	}
	children = append(children, core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 16}, Children: cards})
	return core.View{Style: core.Style{Gap: 10}, Children: children}
}

// card is an item, which opens what it names.
func (sh *shell) card(cx *core.Scope, p *plugin.Plugin, it plugin.Item) core.Node {
	colors := ui.ColorsOf(cx)
	muted := func() core.Style { return core.Style{Color: colors.Muted()} }
	title := core.Text{Content: it.Title, MaxLines: 2, Style: ui.TypeLabel}
	subtitle := core.Text{Content: it.Subtitle, MaxLines: 1, Style: core.Style{FontSize: 12}, StyleFunc: muted}
	press := ui.Press{
		TestID:  "item-" + string(it.Kind) + "-" + it.ID,
		Label:   it.Title,
		OnPress: func() { sh.openItem(p, it) },
		Style:   core.Style{Width: core.Pt(cardWidth), Gap: 6},
	}
	switch it.Kind {
	case plugin.KindVideo, plugin.KindCollection:
		cover := []core.Node{sh.picture(cx, it.Image, 16.0/9, core.Style{})}
		if it.Duration > 0 {
			cover = append(cover, core.Text{
				Content: clock(it.Duration),
				Style: core.Style{
					Position: core.Absolute, Right: core.Pt(6), Bottom: core.Pt(6), Padding: core.Symmetric(1, 5), CornerRadius: 4,
					FontSize: 11, FontVariant: core.FontTabularNums, Color: core.RGB(0xffffff), Background: core.RGBA(0x000000, 0.6),
				},
			})
		}
		press.Children = []core.Node{core.View{Children: cover}, title}
		if it.Subtitle != "" {
			press.Children = append(press.Children, subtitle)
		}
	case plugin.KindUploader:
		press.Style.AlignItems = core.AlignCenter
		press.Children = []core.Node{
			sh.picture(cx, it.Image, 1, core.Style{Width: core.Pt(96), CornerRadius: 48}),
			core.Text{Content: it.Title, MaxLines: 1, Style: ui.TypeLabel.Merge(core.Style{TextAlign: core.TextAlignCenter})},
		}
		if it.Subtitle != "" {
			press.Children = append(press.Children, subtitle)
		}
	default:
		press.Style.Direction = core.Row
		press.Style.AlignItems = core.AlignCenter
		press.Style.Padding = core.Symmetric(10, 12)
		press.Style.CornerRadius = ui.RadiusControl
		press.StyleFunc = ui.Filled(colors.Inset)
		press.Children = []core.Node{
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, Gap: 2}, Children: []core.Node{title}},
			ui.IconOf(lucide.ChevronRight, 16, colors.Muted),
		}
	}
	return press
}

// message is a quiet line in the middle of a page.
func message(cx *core.Scope, text string) core.Node {
	return core.View{Style: core.Style{AlignItems: core.AlignCenter, Padding: core.All(24)}, Children: []core.Node{
		core.Text{TestID: "message", Content: text, Style: core.Style{TextAlign: core.TextAlignCenter},
			StyleFunc: func() core.Style { return core.Style{Color: ui.ColorsOf(cx).Muted()} }},
	}}
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

// errorView says why a call of p failed, with what the viewer can do:
// sign in, or try again.
func (sh *shell) errorView(cx *core.Scope, p *plugin.Plugin, err error, retry func()) core.Node {
	var action core.Node
	switch {
	case errors.Is(err, plugin.ErrSignIn):
		a := sh.account(p)
		action = ui.Button{TestID: "error-sign-in", Text: locales.SignIn(cx), Busy: a.busy.Get, OnPress: func() { sh.signIn(p) }}
	case errors.Is(err, plugin.ErrSetup):
		action = ui.Button{TestID: "error-settings", Text: locales.OpenSettings(cx), Variant: ui.ButtonSecondary, OnPress: sh.openSettings}
	case errors.Is(err, plugin.ErrUnavailable):
	default:
		action = ui.Button{TestID: "retry", Text: locales.Retry(cx), Variant: ui.ButtonSecondary, OnPress: retry}
	}
	children := []core.Node{message(cx, errorText(cx, err))}
	if action != nil {
		children = append(children, action)
	}
	return core.View{TestID: "error", Style: core.Style{AlignItems: core.AlignCenter, Gap: 8}, Children: children}
}

// fail tells the viewer in a toast why a call of p failed.
func (sh *shell) fail(cx *core.Scope, p *plugin.Plugin, err error) {
	t := ui.ToastContent{ID: "error", Type: ui.ToastError, Title: errorText(cx, err)}
	switch {
	case errors.Is(err, plugin.ErrSignIn):
		t.Action, t.OnAction = locales.SignIn(cx), func() { sh.signIn(p) }
	case errors.Is(err, plugin.ErrSetup):
		t.Action, t.OnAction = locales.OpenSettings(cx), sh.openSettings
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
