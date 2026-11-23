package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// videoPage shows a video's details, its parts and related videos, and
// plays its parts.
func (sh *shell) videoPage(cx *core.Scope, p *plugin.Plugin, id string) core.Node {
	video := core.NewSignal[*plugin.Video](cx, nil)
	failed := core.NewSignal[error](cx, nil)
	ctx := cx.Context()
	load := func() {
		failed.Set(nil)
		call(cx, ctx, func(ctx context.Context) (*plugin.Video, error) { return p.Video(ctx, plugin.VideoParams{ID: id}) }, func(v *plugin.Video, err error) {
			if err != nil {
				failed.Set(err)
				return
			}
			sh.retitle(v.Title)
			video.Set(v)
		})
	}
	epoch := sh.account(p).epoch
	cx.Effect(func() {
		epoch.Get()
		cx.Untrack(load)
	})
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if err := failed.Get(); err != nil {
			return sh.errorView(cx, p, err, load)
		}
		v := video.Get()
		if v == nil {
			return core.View{Style: core.Style{AlignItems: core.AlignCenter, Padding: core.All(24)}, Children: []core.Node{ui.Spinner{Size: 24}}}
		}
		return sh.videoDetails(cx, p, v)
	})
}

func (sh *shell) videoDetails(cx *core.Scope, p *plugin.Plugin, v *plugin.Video) core.Node {
	colors := ui.ColorsOf(cx)
	muted := func() core.Style { return core.Style{Color: colors.Muted()} }
	var meta []string
	if v.Published != nil {
		meta = append(meta, v.Published.Local().Format("2006-01-02"))
	}
	if v.Duration > 0 {
		meta = append(meta, clock(v.Duration))
	}
	info := []core.Node{}
	if v.Uploader != nil {
		up := *v.Uploader
		info = append(info, ui.Button{TestID: "uploader", Text: up.Title, Icon: lucide.User, Variant: ui.ButtonGhost, OnPress: func() { sh.openItem(p, up) }})
	}
	if len(meta) > 0 {
		info = append(info, core.Text{Content: strings.Join(meta, " · "), Style: core.Style{FontSize: 13}, StyleFunc: muted})
	}
	info = append(info, core.View{Style: core.Style{AlignItems: core.AlignStart}, Children: []core.Node{
		ui.Button{TestID: "play-video", Text: locales.Play(cx), Icon: lucide.Play, OnPress: func() { sh.play(p, v, firstPart(v)) }},
	}})
	if v.Description != "" {
		info = append(info, core.Text{TestID: "description", Content: v.Description, Selectable: core.SelectableOn})
	}
	children := []core.Node{
		core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 16}, Children: []core.Node{
			sh.picture(cx, v.Image, 16.0/9, core.Style{Width: core.Pt(360)}),
			core.View{Style: core.Style{Gap: 10, FlexGrow: 1, FlexShrink: 1, FlexBasis: core.Pt(240), AlignItems: core.AlignStart}, Children: info},
		}},
	}
	if len(v.Parts) > 1 {
		var parts []core.Node
		for i, pt := range v.Parts {
			parts = append(parts, ui.Button{
				TestID:  "part-" + strconv.Itoa(i+1),
				Text:    locales.PartLabel(cx, i+1, pt.Title),
				Variant: ui.ButtonSecondary,
				OnPress: func() { sh.play(p, v, i) },
			})
		}
		children = append(children, core.View{Style: core.Style{Gap: 10}, Children: []core.Node{
			core.Text{Content: locales.Parts(cx), Style: ui.TypeTitle},
			ui.Wrap{Children: parts},
		}})
	}
	if len(v.Related) > 0 {
		children = append(children, sh.section(cx, p, plugin.Section{Title: locales.Related(cx), Items: v.Related}))
	}
	return ui.ScrollView{
		TestID:       "video-page",
		Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
		ContentStyle: core.Style{Padding: core.All(16), Gap: 24},
		Children:     children,
	}
}

// firstPart is the index of the part playing v starts with.
func firstPart(v *plugin.Video) int {
	for i, pt := range v.Parts {
		if pt.ID == v.Part {
			return i
		}
	}
	return 0
}
