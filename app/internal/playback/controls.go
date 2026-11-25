package playback

import (
	"fmt"
	"strconv"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/oneo-me/nsgo-media/player"

	"github.com/oneo-me/nsgo-media/app/internal/store"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// The controls' widths: in a window, full screen and pinned.
const (
	controlsWidth, fullScreenWidth, miniWidth core.Points = 560, 640, 360
)

// track is a bar the pointer sets a share of, as the seek line and the
// volume are: pressed or dragged, set hears the share there. A seek line
// shows what is buffered and a thumb, and thickens under the pointer.
func track(cx *core.Scope, testID, label, class string, share, buffered func() float64, set func(float64), seekLine bool) core.Node {
	width := core.Points(1)
	at := func(x core.Points) float64 { return float64(min(max(x/width, 0), 1)) }
	pct := func(f float64) core.Length { return core.Pct(float32(min(max(f, 0), 1) * 100)) }
	down := false
	hovered := core.NewSignal(cx, false)
	fill := []core.Node{}
	if buffered != nil {
		fill = append(fill, core.View{Class: "absolute top-0 bottom-0 left-0 bg-white/30", StyleFunc: func() core.Style { return core.Style{Width: pct(buffered())} }})
	}
	fill = append(fill, core.View{Class: "absolute top-0 bottom-0 left-0 rounded-full bg-white", StyleFunc: func() core.Style { return core.Style{Width: pct(share())} }})
	children := []core.Node{core.View{
		Class: "overflow-hidden rounded-full",
		StyleFunc: func() core.Style {
			if !seekLine {
				return core.Style{Height: core.Pt(3), Background: core.RGBA(0xffffff, 0.25)}
			}
			h := core.Points(3)
			if hovered.Get() {
				h = 4
			}
			return core.Style{Height: core.Pt(h), Background: core.RGBA(0xffffff, 0.2)}
		},
		Children: fill,
	}}
	if seekLine {
		children = append(children, core.View{Class: "absolute -ml-1 size-2.5 rounded-full bg-white shadow-md", PointerEvents: core.PointerEventsNone,
			StyleFunc: func() core.Style { return core.Style{Left: pct(share())} }})
	}
	return core.View{
		TestID: testID, Label: label, Role: core.RoleAdjustable,
		Class:        "justify-center " + class,
		OnLayout:     func(b core.Bounds) { width = max(b.Size.Width, 1) },
		OnMouseEnter: func(core.MouseEvent) { hovered.Set(true) },
		OnMouseLeave: func(core.MouseEvent) { hovered.Set(false) },
		OnMouseDown: func(e core.MouseButtonEvent) {
			down = true
			set(at(e.Position.X))
		},
		OnMouseMove: func(e core.MouseEvent) {
			if down && e.PressedButton != core.MouseButtonNone {
				set(at(e.Position.X))
			} else {
				down = false
			}
		},
		OnMouseUp: func(core.MouseButtonEvent) { down = false },
		OnPress:   func(e core.PressEvent) { set(at(e.Position.X)) },
		Desktop:   core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		Children:  children,
	}
}

// controls are the slim bar over the picture's bottom: the seek line
// over one row of playing, the next part, the volume, the time, then
// danmaku, the parts, speed, tracks, quality and full screen. Pinned, it
// keeps playing, the volume, the time, danmaku and full screen. over is
// the pointer over it.
func (v *view) controls(over *core.Signal[bool]) core.Node {
	cx, p, src, menu := v.cx, v.p, v.src, v.menu
	share := func() float64 {
		if d := p.Duration(); d > 0 {
			return p.Position().Seconds() / d.Seconds()
		}
		return 0
	}
	buffered := func() float64 {
		if d := p.Duration(); d > 0 {
			return p.Buffered().Seconds() / d.Seconds()
		}
		return 0
	}
	seek := func(f float64) {
		if d := p.Duration(); d > 0 {
			p.Seek(time.Duration(f*float64(d)), player.Exact)
		}
	}
	toggle := func(name string) func() {
		return func() {
			if menu.GetUntracked() == name {
				menu.Set("")
			} else {
				menu.Set(name)
			}
		}
	}
	// opener is a button that opens a menu above the bar, lit while it is
	// open.
	opener := func(name, testID, label, class string, children ...core.Node) core.View {
		b := darkButton(cx, testID, label, class, toggle(name), children...)
		b.ClassFunc = func() string {
			if menu.Get() == name {
				return "bg-white/25"
			}
			return ""
		}
		return b
	}
	small := "text-[11px] font-semibold text-white"
	timeStyle := core.Style{FontVariant: core.FontTabularNums}
	full := func() bool { return !v.mini() }
	hasParts := func() bool { return full() && len(src().Parts) > 1 && v.pg.OnPart != nil }
	hasNext := func() bool { s := src(); return hasParts() && s.Part+1 < len(s.Parts) }
	hasQualities := func() bool { return full() && v.pg.OnQuality != nil && len(src().Qualities) > 1 }
	hasTracks := func() bool {
		return full() && (countTracks(p, player.Subtitle) > 0 || countTracks(p, player.Audio) > 1)
	}
	hasDanmaku := func() bool { return src().Danmaku != nil }
	when := func(cond func() bool, n core.Node) core.Node {
		return core.Show{When: cond, Then: func(*core.Scope) core.Node { return n }}
	}
	// remaining shows the time left in place of the duration.
	remaining := core.NewSignal(cx, false)
	volumeOver := core.NewSignal(cx, false)
	dmOver := core.NewSignal(cx, false)
	play := labelled(darkButton(cx, "play", "", "size-7", func() { togglePlay(p) }, core.Icon{SVGFunc: func() *core.SVG {
		if playing(p) {
			return lucide.Pause()
		}
		return lucide.Play()
	}, Class: "size-4 text-white"}), func() string {
		if playing(p) {
			return locales.Pause(cx)
		}
		return locales.Play(cx)
	})
	volume := core.View{
		Class:        "h-7 flex-row items-center gap-1 rounded-full hover:bg-white/10",
		OnMouseEnter: func(core.MouseEvent) { volumeOver.Set(true) },
		OnMouseLeave: func(core.MouseEvent) { volumeOver.Set(false) },
		ClassFunc: func() string {
			if v.mini() {
				return "pr-0"
			}
			return "pr-2"
		},
		Children: []core.Node{
			labelled(darkButton(cx, "mute", "", "size-7", func() { p.SetMuted(!p.Muted()) }, core.Icon{SVGFunc: func() *core.SVG {
				if p.Muted() || p.Volume() == 0 {
					return lucide.VolumeX()
				}
				return lucide.Volume2()
			}, Class: "size-3.5 text-white"}), func() string {
				if p.Muted() {
					return locales.Unmute(cx)
				}
				return locales.Mute(cx)
			}),
			core.Show{When: func() bool { return volumeOver.Get() && !v.mini() }, Then: func(cx *core.Scope) core.Node {
				return track(cx, "volume", locales.Volume(cx), "h-4 w-12", func() float64 {
					if p.Muted() {
						return 0
					}
					return p.Volume()
				}, nil, func(f float64) {
					p.SetMuted(false)
					p.SetVolume(f)
				}, false)
			}},
		},
	}
	clockText := core.View{
		TestID: "time", Role: core.RoleButton, Label: locales.ShowTimeLeft(cx),
		OnPress: func(core.PressEvent) { remaining.Set(!remaining.GetUntracked()) },
		Class:   "flex-row items-center gap-1 px-1.5",
		Children: []core.Node{
			core.Text{TestID: "position", ContentFunc: func() string { return clock(p.Position()) }, Class: "text-[11px] font-medium text-white/90", Style: timeStyle},
			core.Text{Content: "/", Class: "text-[11px] text-white/40"},
			core.Text{TestID: "duration", ContentFunc: func() string {
				if remaining.Get() {
					return "−" + clock(max(p.Duration()-p.Position(), 0))
				}
				return clock(p.Duration())
			}, Class: "text-[11px] font-medium text-white/50", Style: timeStyle},
		},
	}
	dmButton := labelled(darkButton(cx, "danmaku-toggle", "", "h-7 flex-row gap-1 px-2", func() {
		v.setDanmaku(func(d *store.Danmaku) { d.Off = !d.Off })
	},
		core.Icon{SVG: lucide.MessageSquareText(), Class: "size-3.5", StyleFunc: func() core.Style {
			if v.danmaku.Get().Off {
				return core.Style{Color: core.RGBA(0xffffff, 0.5)}
			}
			return core.Style{Color: core.RGB(0x66ccff)}
		}},
		core.Text{Content: "DM", ClassFunc: func() string {
			if v.danmaku.Get().Off {
				return "text-[11px] font-semibold text-white/60"
			}
			return small
		}},
		core.Show{When: func() bool { return !v.danmaku.Get().Off }, Then: func(*core.Scope) core.Node {
			return core.View{Class: "size-1 rounded-full bg-[#66CCFF]"}
		}},
	), func() string {
		if v.danmaku.Get().Off {
			return locales.ShowDanmaku(cx)
		}
		return locales.HideDanmaku(cx)
	})
	dmButton.OnLongPress = func(core.PressEvent) { toggle("danmaku")() }
	dm := core.View{
		TestID:       "danmaku",
		Class:        "flex-row items-center rounded-full",
		OnMouseEnter: func(core.MouseEvent) { dmOver.Set(true) },
		OnMouseLeave: func(core.MouseEvent) { dmOver.Set(false) },
		ClassFunc: func() string {
			if menu.Get() == "danmaku" {
				return "bg-white/25"
			}
			return ""
		},
		Children: []core.Node{
			dmButton,
			core.Show{When: func() bool { return dmOver.Get() || menu.Get() == "danmaku" }, Then: func(cx *core.Scope) core.Node {
				return darkButton(cx, "danmaku-settings", locales.DanmakuSettings(cx), "size-7", toggle("danmaku"),
					core.Icon{SVG: lucide.ChevronUp(), Class: "size-3.5 text-white"})
			}},
		},
	}
	row := core.View{Class: "h-7 flex-row items-center gap-0.5", Children: []core.Node{
		play,
		when(hasNext, darkButton(cx, "next", locales.NextPart(cx), "size-7", func() { v.pg.OnPart(src().Part + 1) },
			core.Icon{SVG: lucide.SkipForward(), Class: "size-3.5 text-white"})),
		volume,
		clockText,
		core.View{Class: "flex-1"},
		when(hasDanmaku, dm),
		when(hasParts, opener("parts", "parts", locales.Parts(cx), "h-7 flex-row gap-1 px-2",
			core.Icon{SVG: lucide.ListVideo(), Class: "size-3.5 text-white"},
			core.Text{ContentFunc: func() string {
				s := src()
				return fmt.Sprintf("%s/%d", s.Parts[min(s.Part, len(s.Parts)-1)].Label, len(s.Parts))
			}, Class: "text-[11px] font-medium text-white"})),
		when(full, opener("speed", "rate", locales.Speed(cx), "h-7 min-w-7 px-1.5",
			core.Text{ContentFunc: func() string { return rateLabel(p.Rate()) }, Class: small})),
		when(hasTracks, opener("tracks", "tracks", locales.SubtitlesAndAudio(cx), "size-7",
			core.Icon{SVG: lucide.Captions(), Class: "size-3.5 text-white"})),
		when(hasQualities, opener("quality", "quality", locales.Quality(cx), "h-7 px-2",
			core.Text{ContentFunc: func() string { return qualityLabel(src()) }, Class: small})),
		labelled(darkButton(cx, "fullscreen", "", "size-7", func() { desktop.ToggleFullScreen(cx) }, core.Icon{SVGFunc: func() *core.SVG {
			if v.fullScreen() {
				return lucide.Minimize()
			}
			return lucide.Maximize()
		}, Class: "size-3.5 text-white"}), func() string {
			if v.fullScreen() {
				return locales.ExitFullScreen(cx)
			}
			return locales.FullScreen(cx)
		}),
	}}
	look := glass(cx, core.GlassClear, true)
	return core.View{
		Class: "absolute right-0 left-0 items-center px-3",
		StyleFunc: func() core.Style {
			switch {
			case v.fullScreen():
				return core.Style{Bottom: core.Pt(28)}
			case v.pinned.Get():
				return core.Style{Bottom: core.Pt(10)}
			}
			return core.Style{Bottom: core.Pt(12)}
		},
		// The space beside the bar lets the pointer through to the
		// picture.
		PointerEvents: core.PointerEventsBoxNone,
		Children: []core.Node{core.View{
			TestID: "controls", Ref: v.at.panel,
			Class:        "max-w-full gap-1 overflow-hidden rounded-2xl px-2 pt-2 pb-1.5 shadow-xl",
			Desktop:      core.DesktopViewProps{WindowDrag: core.WindowDragOff},
			OnMouseEnter: func(core.MouseEvent) { over.Set(true) },
			OnMouseLeave: func(core.MouseEvent) { over.Set(false) },
			StyleFunc: func() core.Style {
				w := controlsWidth
				switch {
				case v.fullScreen():
					w = fullScreenWidth
				case v.pinned.Get():
					w = miniWidth
				}
				return look().Merge(core.Style{Width: core.Pt(w)})
			},
			Children: []core.Node{
				core.View{Class: "h-2.5 justify-center px-1", Children: []core.Node{
					track(cx, "seek", locales.Position(cx), "h-2.5", share, buffered, seek, true),
				}},
				row,
			},
		}},
	}
}

// countTracks counts the source's tracks of kind.
func countTracks(p *player.Player, kind player.Kind) int {
	info := p.Info()
	if info == nil {
		return 0
	}
	n := 0
	for _, t := range info.Tracks {
		if t.Kind == kind {
			n++
		}
	}
	return n
}

func rateLabel(r float64) string { return fmt.Sprintf("%g×", r) }

// qualityLabel is the label of the quality the source is at.
func qualityLabel(s Source) string {
	for _, q := range s.Qualities {
		if q.ID == s.Quality {
			return q.Label
		}
	}
	return s.Quality
}

// Where each menu's right edge lies from the controls', as the designs
// place them.
const (
	speedRight, qualityRight, tracksRight, danmakuRight core.Points = -78, 16, 66, -88
	// menuGap is the space between a menu and the controls.
	menuGap core.Points = 10
)

// menus are the panels the controls open over the picture, closing as
// one is chosen or the picture is pressed.
func (v *view) menus() core.Node {
	p, src, menu := v.p, v.src, v.menu
	return core.Dynamic(func(cx *core.Scope) core.Node {
		switch menu.Get() {
		case "speed":
			var rows []core.Node
			for i, r := range rates {
				rows = append(rows, choice(fmt.Sprintf("rate-%d", i), rateLabel(r), func() bool { return p.Rate() == r }, func() {
					p.SetRate(r)
					menu.Set("")
				}))
			}
			return panel(cx, v.at.above(menuGap, speedRight), "w-28", locales.Speed(cx), rows...)
		case "quality":
			var rows []core.Node
			for i, q := range src().Qualities {
				rows = append(rows, choice(fmt.Sprintf("quality-%d", i), q.Label, func() bool { return src().Quality == q.ID }, func() {
					menu.Set("")
					if q.ID != src().Quality && v.pg.OnQuality != nil {
						v.pg.OnQuality(q.ID, p.Position())
					}
				}))
			}
			return panel(cx, v.at.above(menuGap, qualityRight), "w-40", locales.Quality(cx), rows...)
		case "tracks":
			return tracksPanel(cx, p, v.at.above(menuGap, tracksRight))
		case "parts":
			return partsPanel(cx, src, menu, v.pg, v.at)
		case "danmaku":
			return v.danmakuPanel(cx)
		}
		return nil
	})
}

// menuGlass is the look of a menu over the picture: regular glass,
// denser than the controls' for reading, or a dark fill over a blur
// where the system draws no Liquid Glass.
func menuGlass(cx *core.Scope, place func() core.Style) func() core.Style {
	return func() core.Style {
		st := core.Style{BackdropFilters: []core.Filter{core.Blur(40), core.Saturate(1.5)}}
		if systemGlass(cx) {
			st.GlassEffect = core.GlassRegular
		} else {
			st.Background = core.RGBA(0x18181b, 0.65)
			st.BorderWidth, st.BorderColor = 1, core.RGBA(0xffffff, 0.2)
		}
		return st.Merge(place())
	}
}

// panel is a menu over the picture, above the controls.
func panel(cx *core.Scope, place func() core.Style, class, title string, rows ...core.Node) core.Node {
	return core.View{
		TestID:    "menu",
		Class:     "absolute gap-0.5 rounded-[18px] p-1.5 shadow-2xl " + class,
		Desktop:   core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		StyleFunc: menuGlass(cx, place),
		Children: append([]core.Node{
			core.Text{Content: title, Class: "px-2.5 pt-1 pb-1 text-[11px] font-semibold text-white/50"},
		}, rows...),
	}
}

// choice is a row of a menu over the picture, checked while it is the
// one chosen.
func choice(testID, label string, checked func() bool, choose func()) core.Node {
	return core.View{
		TestID: testID, Role: core.RoleMenuItemRadio, Label: label, Selected: checked,
		OnPress: func(core.PressEvent) { choose() },
		Class:   "group h-7 flex-row items-center gap-2 rounded-xl px-2.5 hover:bg-white/10 selected:bg-white/15",
		Children: []core.Node{
			core.Text{Content: label, MaxLines: 1, Class: "flex-1 text-xs text-white/90 group-selected:font-medium group-selected:text-white"},
			core.Show{When: checked, Then: func(*core.Scope) core.Node {
				return core.Icon{SVG: lucide.Check(), Class: "size-3.5 text-white"}
			}},
		},
	}
}

// tracksPanel chooses the subtitle and audio tracks, each by its title
// or else its number.
func tracksPanel(cx *core.Scope, p *player.Player, place func() core.Style) core.Node {
	column := func(title string, rows []core.Node) core.Node {
		return core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0)}, Children: append([]core.Node{
			core.Text{Content: title, Class: "px-2.5 pt-1 pb-1 text-[11px] font-semibold text-white/50"},
		}, rows...)}
	}
	subs := []core.Node{choice("subtitle-off", locales.SubtitlesOff(cx), func() bool { return p.SubtitleTrack() < 0 }, func() { p.SelectSubtitle(-1) })}
	var audio []core.Node
	info := p.Info()
	if info != nil {
		ns, na := 0, 0
		for _, t := range info.Tracks {
			switch t.Kind {
			case player.Subtitle:
				ns++
				subs = append(subs, choice("subtitle-"+strconv.Itoa(t.ID), trackLabel(cx, t, ns), func() bool { return p.SubtitleTrack() == t.ID }, func() { p.SelectSubtitle(t.ID) }))
			case player.Audio:
				na++
				audio = append(audio, choice("audio-"+strconv.Itoa(t.ID), trackLabel(cx, t, na), func() bool { return p.AudioTrack() == t.ID }, func() { p.SelectAudio(t.ID) }))
			}
		}
	}
	columns := []core.Node{column(locales.Subtitles(cx), subs)}
	if len(audio) > 0 {
		columns = append(columns, core.View{Class: "w-px bg-white/10"}, column(locales.Audio(cx), audio))
	}
	return core.View{
		TestID:    "menu",
		Class:     "absolute w-[340px] flex-row gap-1 rounded-[18px] p-1.5 shadow-2xl",
		Desktop:   core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		StyleFunc: menuGlass(cx, place),
		Children:  columns,
	}
}

// trackLabel is a track's title, else its number among its kind, with
// its language.
func trackLabel(cx *core.Scope, t player.Track, n int) string {
	label := t.Title
	if label == "" {
		label = locales.Track(cx, n)
	}
	if t.Language != "" && t.Language != "und" {
		label += " (" + t.Language + ")"
	}
	return label
}

// partsPanel lists the video's parts down the picture's right, from
// under the top bar to above the controls; choosing one plays it.
func partsPanel(cx *core.Scope, src func() Source, menu *core.Signal[string], pg Page, at *anchors) core.Node {
	s := src()
	var rows []core.Node
	for i, pt := range s.Parts {
		current := i == s.Part
		label, title, sub := "w-5 text-[11px] font-semibold text-white/50", "text-xs text-white/90", "text-[11px] text-white/40"
		box := "flex-row items-center gap-2.5 rounded-xl px-2.5 py-2 hover:bg-white/10"
		if current {
			label, title, sub = "w-5 text-[11px] font-semibold text-white", "text-xs font-medium text-white", "text-[11px] text-white/60"
			box += " bg-white/15"
		}
		text := []core.Node{core.Text{Content: pt.Title, MaxLines: 1, Class: title}}
		if pt.Duration > 0 {
			text = append(text, core.Text{Content: clock(pt.Duration), Class: sub})
		}
		children := []core.Node{
			core.Text{Content: pt.Label, Class: label},
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: text},
		}
		if current {
			children = append(children, core.Icon{SVG: lucide.AudioLines(), Class: "size-3.5 text-white"})
		}
		rows = append(rows, core.View{
			TestID: "part-" + strconv.Itoa(i), Role: core.RoleButton, Label: pt.Label + " " + pt.Title,
			OnPress: func(core.PressEvent) {
				menu.Set("")
				if i != src().Part {
					pg.OnPart(i)
				}
			},
			Class: box, Children: children,
		})
	}
	place := func() core.Style {
		return core.Style{Bottom: at.above(menuGap, 0)().Bottom}
	}
	return core.View{
		TestID:    "menu",
		Class:     "absolute top-14 right-3.5 w-[272px] gap-1 rounded-[20px] p-2 shadow-2xl",
		Desktop:   core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		StyleFunc: menuGlass(cx, place),
		Children: []core.Node{
			core.Text{Content: locales.PartsN(cx, len(s.Parts)), Class: "px-2.5 pt-1 pb-1.5 text-xs font-semibold text-white"},
			ui.ScrollView{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, ContentStyle: core.Style{Gap: 4}, Children: rows},
		},
	}
}

// danmakuPanel sets how danmaku shows: on or off, its kinds, opacity,
// the area it fills, its size and its speed.
func (v *view) danmakuPanel(cx *core.Scope) core.Node {
	d := v.danmaku.Get
	set := v.setDanmaku
	heading := func(text string) core.Node {
		return core.Text{Content: text, Class: "text-[11px] font-medium text-white/50"}
	}
	kind := func(testID, label string, icon func() *core.SVG, tint string, hidden func(store.Danmaku) bool, flip func(*store.Danmaku)) core.Node {
		return core.View{
			TestID: testID, Role: core.RoleCheckbox, Label: label,
			Selected: func() bool { return !hidden(d()) },
			OnPress:  func(core.PressEvent) { set(flip) },
			ClassFunc: func() string {
				if hidden(d()) {
					return "flex-1 items-center gap-1 rounded-xl bg-white/5 py-2 opacity-50"
				}
				return "flex-1 items-center gap-1 rounded-xl bg-white/15 py-2"
			},
			Children: []core.Node{
				core.Icon{SVG: icon(), Class: "size-3.5 " + tint},
				core.Text{Content: label, Class: "text-[11px] font-medium text-white"},
			},
		}
	}
	segmented := func(testID string, options []string, chosen func() int, choose func(int)) core.Node {
		var items []core.Node
		for i, o := range options {
			items = append(items, core.View{
				TestID: testID + "-" + strconv.Itoa(i), Role: core.RoleRadio, Label: o,
				Selected: func() bool { return chosen() == i },
				OnPress:  func(core.PressEvent) { choose(i) },
				Class:    "group flex-1 items-center rounded-lg py-1 selected:bg-white/20",
				Children: []core.Node{core.Text{Content: o, Class: "text-[11px] font-medium text-white/70 group-selected:font-semibold group-selected:text-white"}},
			})
		}
		return core.View{Class: "flex-row gap-0.5 rounded-xl bg-white/10 p-0.5", Children: items}
	}
	on := ui.Switch{TestID: "danmaku-on", Label: locales.Danmaku(cx), Checked: func() bool { return !d().Off }, OnChange: func(b bool) {
		set(func(s *store.Danmaku) { s.Off = !b })
	}}
	opacity := core.View{Class: "gap-1.5", Children: []core.Node{
		core.View{Class: "flex-row items-center justify-between", Children: []core.Node{
			heading(locales.Opacity(cx)),
			core.Text{ContentFunc: func() string { return fmt.Sprintf("%d%%", int(d().Opacity()*100+0.5)) }, Class: "text-[11px] font-semibold text-white/90"},
		}},
		track(cx, "danmaku-opacity", locales.Opacity(cx), "h-3.5", func() float64 { return (d().Opacity() - 0.1) / 0.9 }, nil, func(f float64) {
			set(func(s *store.Danmaku) { s.SetOpacity(0.1 + f*0.9) })
		}, true),
	}}
	return core.View{
		TestID:    "menu",
		Class:     "absolute w-[308px] gap-3 rounded-[20px] p-3.5 shadow-2xl",
		Desktop:   core.DesktopViewProps{WindowDrag: core.WindowDragOff},
		StyleFunc: menuGlass(cx, v.at.above(menuGap, danmakuRight)),
		Children: []core.Node{
			core.View{Class: "flex-row items-center justify-between", Children: []core.Node{
				core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
					core.Icon{SVG: lucide.MessageSquareText(), Class: "size-4 text-[#66CCFF]"},
					core.Text{Content: locales.Danmaku(cx), Class: "text-[13px] font-semibold text-white"},
				}},
				on,
			}},
			core.View{Class: "gap-1.5", Children: []core.Node{
				heading(locales.DanmakuKinds(cx)),
				core.View{Class: "flex-row gap-1.5", Children: []core.Node{
					kind("danmaku-scroll", locales.DanmakuScroll(cx), lucide.MoveHorizontal, "text-[#66CCFF]", func(s store.Danmaku) bool { return s.HideScroll }, func(s *store.Danmaku) { s.HideScroll = !s.HideScroll }),
					kind("danmaku-top", locales.DanmakuTop(cx), lucide.ArrowUpToLine, "text-[#FFE666]", func(s store.Danmaku) bool { return s.HideTop }, func(s *store.Danmaku) { s.HideTop = !s.HideTop }),
					kind("danmaku-bottom", locales.DanmakuBottom(cx), lucide.ArrowDownToLine, "text-white", func(s store.Danmaku) bool { return s.HideBottom }, func(s *store.Danmaku) { s.HideBottom = !s.HideBottom }),
					kind("danmaku-color", locales.DanmakuColor(cx), lucide.Palette, "text-[#FF85B3]", func(s store.Danmaku) bool { return s.HideColor }, func(s *store.Danmaku) { s.HideColor = !s.HideColor }),
				}},
			}},
			opacity,
			core.View{Class: "gap-1.5", Children: []core.Node{
				heading(locales.DanmakuArea(cx)),
				segmented("danmaku-area", []string{"1/4", "1/2", "3/4", locales.DanmakuFull(cx)}, func() int { return d().Quarters() - 1 }, func(i int) {
					set(func(s *store.Danmaku) { s.Area = i + 1 })
				}),
			}},
			core.View{Class: "flex-row gap-2", Children: []core.Node{
				core.View{Class: "flex-1 gap-1.5", Children: []core.Node{
					heading(locales.DanmakuSize(cx)),
					segmented("danmaku-size", []string{locales.SizeSmall(cx), locales.SizeMedium(cx), locales.SizeLarge(cx)}, func() int { return d().Size + 1 }, func(i int) {
						set(func(s *store.Danmaku) { s.Size = i - 1 })
					}),
				}},
				core.View{Class: "flex-1 gap-1.5", Children: []core.Node{
					heading(locales.Speed(cx)),
					segmented("danmaku-speed", []string{locales.SpeedSlow(cx), "1×", locales.SpeedFast(cx)}, func() int { return d().Speed + 1 }, func(i int) {
						set(func(s *store.Danmaku) { s.Speed = i - 1 })
					}),
				}},
			}},
		},
	}
}

// clock writes d as m:ss or h:mm:ss.
func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
