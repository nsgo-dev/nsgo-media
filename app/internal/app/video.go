package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/i18n"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// videoPage shows a video's details, its parts and related videos, and
// plays its parts.
func (sh *shell) videoPage(cx *core.Scope, p *plugin.Plugin, id string) core.Node {
	video := core.NewSignal[*plugin.Video](cx, nil)
	failed := core.NewSignalFunc[error](cx, nil, nil)
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
			if errors.Is(err, plugin.ErrUnavailable) {
				return state(stateQuiet, lucide.VideoOff, errorText(cx, err), locales.CannotBePlayed(cx, p.Name()),
					ui.Button{TestID: "unavailable-back", Text: locales.Back(cx), Variant: ui.ButtonOutline, OnPress: sh.back})
			}
			return sh.failure(cx, p, err, load)
		}
		v := video.Get()
		if v == nil {
			return videoLoading()
		}
		return sh.videoDetails(cx, p, v)
	})
}

// videoLoading stands in for a video's page while it loads.
func videoLoading() core.Node {
	parts := []core.Node{}
	for range 3 {
		parts = append(parts, core.View{Class: "h-14 w-[290px] rounded-xl bg-chip"})
	}
	return core.View{TestID: "page-loading", Class: "flex-1 gap-7 overflow-hidden px-7 py-6", Children: []core.Node{
		core.View{Class: "flex-row gap-7", Children: []core.Node{
			core.View{Class: "h-[225px] w-[400px] rounded-2xl bg-skeleton"},
			core.View{Class: "flex-1 gap-3 py-1", Children: []core.Node{
				core.View{Class: "h-6 w-4/5 rounded-md bg-skeleton"},
				core.View{Class: "h-6 w-1/2 rounded-md bg-skeleton"},
				core.View{Class: "h-7 w-48 rounded-full bg-chip"},
				core.View{Class: "h-10 w-40 rounded-xl bg-skeleton"},
				core.View{Class: "h-3 w-full rounded bg-chip"},
				core.View{Class: "h-3 w-11/12 rounded bg-chip"},
			}},
		}},
		core.View{Class: "flex-row gap-3", Children: parts},
	}}
}

// resume is where a video was left: its part and the position in it.
type resume struct {
	part int
	at   time.Duration
}

// resumeOf is where the viewer left v, the part last played, from the
// resume positions kept.
func (sh *shell) resumeOf(p *plugin.Plugin, v *plugin.Video) (resume, bool) {
	sh.played.Get()
	if sh.positions == nil {
		return resume{}, false
	}
	key, at, ok := sh.positions.Latest(videoKey(p, v.ID))
	if !ok {
		return resume{}, false
	}
	for i := range v.Parts {
		if partKey(p, v, i) == key {
			return resume{part: i, at: at}, true
		}
	}
	return resume{}, false
}

func (sh *shell) videoDetails(cx *core.Scope, p *plugin.Plugin, v *plugin.Video) core.Node {
	// play plays v as Play does: where it was left, or the part the
	// plugin names.
	play := func() {
		if n := nextEpisode(v); n >= 0 {
			sh.play(p, v, n, 0, false)
			return
		}
		if v.Part == "" {
			if r, ok := sh.resumeOf(p, v); ok {
				sh.play(p, v, r.part, r.at, false)
				return
			}
		}
		sh.play(p, v, firstPart(v), 0, false)
	}
	picture := core.View{
		TestID: "video-picture", Role: core.RoleButton, Label: locales.Play(cx), OnPress: func(core.PressEvent) { play() },
		Class: "h-[225px] w-[400px] overflow-hidden rounded-2xl bg-well shadow-lg",
		Children: []core.Node{
			sh.cover(cx, v.Image, "absolute inset-0"),
			core.View{Class: "absolute top-2.5 left-2.5", Children: []core.Node{sh.badge(p, badgeCover)}},
			core.View{Class: "absolute inset-0 items-center justify-center bg-black/10", Children: []core.Node{
				core.View{Class: "size-16 items-center justify-center rounded-full bg-white/85 shadow-xl", Children: []core.Node{
					core.Icon{SVG: lucide.Play(), Class: "ml-1 size-7 text-zinc-900"},
				}},
			}},
			bar("h-1", func() float64 {
				r, ok := sh.resumeOf(p, v)
				if !ok || v.Parts[r.part].Duration <= 0 {
					return 0
				}
				return r.at.Seconds() / v.Parts[r.part].Duration
			}),
		},
	}

	var meta []string
	if v.Published != nil {
		meta = append(meta, i18n.FormatDate(i18n.Language(cx), v.Published.Local(), i18n.DateOptions{Date: i18n.MediumStyle}))
	}
	if v.Duration > 0 {
		meta = append(meta, clock(v.Duration))
	}
	switch {
	case v.Show != nil:
		meta = append(meta, locales.EpisodesCount(cx, len(v.Parts)))
	case len(v.Parts) > 1:
		meta = append(meta, locales.PartsCount(cx, len(v.Parts)))
	}
	line := []core.Node{}
	if v.Uploader != nil {
		up := *v.Uploader
		line = append(line, core.View{
			TestID: "uploader", Role: core.RoleButton, Label: up.Title, OnPress: func(core.PressEvent) { sh.openItem(p, up) },
			Class: "flex-row items-center gap-2 rounded-full bg-chip py-1 pr-3 pl-1 hover:bg-well",
			Children: []core.Node{
				sh.cover(cx, up.Image, "size-6 rounded-full bg-well"),
				core.Text{Content: up.Title, MaxLines: 1, Class: "text-[13px] font-medium text-foreground"},
			},
		})
	}
	line = append(line, core.View{
		TestID: "video-source",
		Class:  "flex-row items-center gap-1.5 rounded-full border border-border bg-background py-0.5 pr-2.5 pl-1",
		Children: []core.Node{
			sh.badge(p, badgeSmall),
			core.Text{Content: p.Name(), MaxLines: 1, Class: "text-xs font-medium text-muted-foreground"},
		},
	})
	if len(meta) > 0 {
		line = append(line, core.Text{Content: strings.Join(meta, " · "), Class: "text-[13px] text-muted-foreground"})
	}

	actions := core.Dynamic(func(cx *core.Scope) core.Node {
		r, resumable := sh.resumeOf(p, v)
		label := locales.Play(cx)
		if resumable && v.Part == "" {
			if len(v.Parts) > 1 {
				label = locales.ResumePartAt(cx, r.part+1, clock(r.at.Seconds()))
			} else {
				label = locales.ResumeAt(cx, clock(r.at.Seconds()))
			}
		}
		next := nextEpisode(v)
		if next >= 0 {
			label = locales.WatchEpisode(cx, next+1)
		}
		buttons := []core.Node{ui.Button{TestID: "play-video", Text: label, Size: ui.SizeLarge, OnPress: play}}
		if resumable && v.Part == "" && next < 0 {
			buttons = append(buttons, ui.Button{
				TestID: "play-from-start", Text: locales.PlayFromStart(cx), Icon: lucide.RotateCcw, Variant: ui.ButtonOutline, Size: ui.SizeLarge,
				OnPress: func() { sh.play(p, v, r.part, 0, true) },
			})
		}
		return core.View{Class: "flex-row items-center gap-2", Children: buttons}
	})
	row := []core.Node{actions}
	if v.Show != nil && p.Has(plugin.ExportFollow) {
		row = append(row, sh.followButton(cx, p, v.Show))
	}

	info := []core.Node{
		core.Text{Content: v.Title, MaxLines: 2, Class: "text-[22px] leading-tight font-bold text-foreground"},
	}
	if len(line) > 0 {
		info = append(info, core.View{Class: "flex-row flex-wrap items-center gap-3", Children: line})
	}
	info = append(info, core.View{Class: "flex-row items-center gap-2 pt-1", Children: row})
	if v.Description != "" {
		expanded := core.NewSignal(cx, false)
		info = append(info,
			core.Dynamic(func(cx *core.Scope) core.Node {
				return core.Text{TestID: "description", Content: v.Description, Selectable: core.SelectableOn, MaxLines: clamp(expanded.Get()),
					Class: "pt-1 text-[13px] leading-relaxed text-muted-foreground"}
			}),
			core.Show{
				When: func() bool { return !expanded.Get() && len(v.Description) > 160 },
				Then: func(cx *core.Scope) core.Node {
					return core.Text{TestID: "description-more", Content: locales.More(cx), Class: "text-[13px] font-medium text-accent",
						Role: core.RoleButton, OnPress: func(core.PressEvent) { expanded.Set(true) }}
				},
			},
		)
	}
	children := []core.Node{
		core.View{Class: "flex-row gap-7", Children: []core.Node{
			picture,
			core.View{Class: "flex-1 gap-3 py-1", Style: core.Style{FlexShrink: 1}, Children: info},
		}},
	}
	if len(v.Parts) > 1 {
		var parts []core.Node
		for i, pt := range v.Parts {
			parts = append(parts, sh.partRow(cx, p, v, i, pt))
		}
		title := locales.Parts(cx)
		if v.Show != nil {
			title = locales.Episodes(cx)
		}
		children = append(children, core.View{Class: "gap-3", Children: []core.Node{
			sectionHeader(cx, title, "", nil),
			core.View{Class: "flex-row flex-wrap gap-3", Children: parts},
		}})
	}
	if len(v.Related) > 0 {
		s := section{title: locales.Related(cx)}
		for _, it := range v.Related {
			s.items = append(s.items, entry{p: p, it: it})
		}
		children = append(children, sh.section(cx, s))
	}
	return ui.ScrollView{
		TestID:       "video-page",
		Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
		ContentStyle: core.Style{Padding: core.Symmetric(24, 28), Gap: 28},
		Children:     children,
	}
}

// clamp is the lines a description shows.
func clamp(expanded bool) int {
	if expanded {
		return 0
	}
	return 3
}

// partRow is a part of v: its number, title and length, marked when it
// is the part last played, and how far the viewer got in it.
func (sh *shell) partRow(cx *core.Scope, p *plugin.Plugin, v *plugin.Video, i int, pt plugin.Part) core.Node {
	current := func() bool {
		r, ok := sh.resumeOf(p, v)
		return ok && r.part == i
	}
	length := ""
	if pt.Duration > 0 {
		length = clock(pt.Duration)
	}
	text := []core.Node{core.Text{Content: pt.Title, MaxLines: 1, Class: "text-[13px] font-medium text-foreground"}}
	if length != "" {
		text = append(text, core.Text{Content: length, Class: "text-xs text-muted-foreground group-selected:text-accent"})
	}
	number, label := episodeLabels(cx, v, i)
	return core.View{
		TestID: "part-" + strconv.Itoa(i+1), Role: core.RoleButton, Label: label,
		OnPress:  func(core.PressEvent) { sh.play(p, v, i, 0, false) },
		Selected: current,
		Class:    "group h-14 w-[290px] flex-row items-center gap-3 overflow-hidden rounded-xl border border-border bg-background px-3 hover:bg-surface-hover selected:border-accent/50 selected:bg-accent/[0.06]",
		Children: []core.Node{
			core.View{Class: "h-7 min-w-9 items-center justify-center rounded-lg bg-chip px-1.5 group-selected:bg-accent", Children: []core.Node{
				core.Text{Content: number, Class: "text-xs font-semibold text-muted-foreground group-selected:text-white"},
			}},
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: text},
			core.Show{When: current, Then: func(*core.Scope) core.Node {
				return core.Icon{SVG: lucide.AudioLines(), Class: "size-4 text-accent"}
			}},
			bar("h-[3px]", func() float64 {
				sh.played.Get()
				if sh.positions == nil || pt.Duration <= 0 {
					return 0
				}
				at, ok := sh.positions.Get(partKey(p, v, i))
				if !ok {
					return 0
				}
				return at.Seconds() / pt.Duration
			}),
		},
	}
}

// firstPart is the index of the part the plugin names, else the first.
func firstPart(v *plugin.Video) int {
	for i, pt := range v.Parts {
		if pt.ID == v.Part {
			return i
		}
	}
	return 0
}
