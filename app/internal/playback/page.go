package playback

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/oneo-me/nsgo-media/player"

	"github.com/oneo-me/nsgo-media/app/internal/store"
	"github.com/oneo-me/nsgo-media/app/locales"
)

var rates = []float64{0.5, 0.75, 1, 1.25, 1.5, 2}

const (
	// saveEvery is how far playback moves between saved positions.
	saveEvery = 5 * time.Second
	// seekStep and volumeStep are what the arrow keys move.
	seekStep   = 5 * time.Second
	volumeStep = 0.1
	// offerFor is how long the resume offer shows, stillFor how long the
	// pointer rests before the bars and the pointer hide, and fadeFor how
	// long they take to.
	offerFor = 10 * time.Second
	stillFor = 1500 * time.Millisecond
	fadeFor  = 150 * time.Millisecond
)

// The player window's sizes: as it opens, and pinned on top.
const (
	WindowWidth, WindowHeight core.Points = 960, 540
	pinnedWidth, pinnedHeight core.Points = 560, 315
)

// Page plays a source with the app's controls, reopening as Source
// changes, and keeps its resume position in Positions. In a window of its
// own (Window), it is the whole window: dragging the picture moves it,
// and it pins itself above the other windows.
type Page struct {
	Source    func() Source
	Positions *store.Positions
	// Settings keep how danmaku shows; nil keeps it while the page shows.
	Settings *store.Settings
	OnClose  func()
	// Window is set when the page fills a player window of its own,
	// borderless and without the system's buttons.
	Window bool
	// Client downloads subtitles; nil is a client with a timeout.
	Client *http.Client
	// OnQuality asks for the source at another of its qualities, to start
	// at the position reached.
	OnQuality func(id string, at time.Duration)
	// OnPosition hears the position of the source of key as the page
	// saves it, and OnEnded that the source played to its end.
	OnPosition func(key string, pos, dur time.Duration)
	OnEnded    func()
	// OnPart asks for another of the source's parts, by its index.
	OnPart func(i int)
}

// view is what the page's pieces share.
type view struct {
	cx      *core.Scope
	p       *player.Player
	pg      Page
	src     func() Source
	menu    *core.Signal[string]
	at      *anchors
	dm      *danmaku
	pinned  *core.Signal[bool]
	failed  func() bool
	danmaku *core.Signal[store.Danmaku]
}

// fullScreen reports whether the page's window is full screen.
func (v *view) fullScreen() bool { return desktop.FullScreen(v.cx) }

// mini reports whether the controls are the pinned window's few.
func (v *view) mini() bool { return v.pinned.Get() && !v.fullScreen() }

// setDanmaku changes how danmaku shows, keeping it.
func (v *view) setDanmaku(change func(*store.Danmaku)) {
	d := v.danmaku.GetUntracked()
	change(&d)
	v.danmaku.Set(d)
	if s := v.pg.Settings; s != nil {
		go func() {
			if err := s.SetDanmaku(d); err != nil {
				log.Printf("danmaku settings: %v", err)
			}
		}()
	}
}

// pin keeps the window above the others, at its pinned size, or puts it
// back among them at the size it had.
func (v *view) pin(on bool, before *core.Size) {
	cx := v.cx
	if !v.pg.Window || on == v.pinned.GetUntracked() || v.fullScreen() {
		return
	}
	v.pinned.Set(on)
	if !desktop.IsAvailable(cx) {
		return
	}
	if on {
		f := desktop.Frame(cx)
		*before = core.Size{Width: f.Width, Height: f.Height}
		desktop.SetWindowLevel(cx, desktop.WindowLevelFloating)
		desktop.SetWindowFrame(cx, desktop.WindowFrame{Width: pinnedWidth, Height: pinnedHeight, Animated: true})
		return
	}
	desktop.SetWindowLevel(cx, desktop.WindowLevelNormal)
	if before.Width > 0 {
		desktop.SetWindowFrame(cx, desktop.WindowFrame{Width: before.Width, Height: before.Height, Animated: true})
	}
}

// Render implements core.Node.
func (pg Page) Render(cx *core.Scope) core.Node {
	p := player.New(cx)
	invalid := core.NewSignal(cx, false)
	// offer is the stored position the viewer may resume at, 0 for none.
	offer := core.NewSignal(cx, time.Duration(0))
	current := core.NewSignal(cx, &Source{})
	// started is set once the source played, before which the bars stay.
	started := core.NewSignal(cx, false)
	client := pg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var dm store.Danmaku
	if pg.Settings != nil {
		dm = pg.Settings.Danmaku()
	}
	v := &view{cx: cx, p: p, pg: pg, menu: core.NewSignal(cx, ""), pinned: core.NewSignal(cx, false), danmaku: core.NewSignal(cx, dm)}
	v.src = func() Source { return *current.Get() }
	v.failed = func() bool { return invalid.Get() || p.State() == player.Failed }
	v.at = &anchors{page: &core.Ref{}, panel: &core.Ref{}, layout: core.NewSignal(cx, 0)}
	v.dm = newDanmaku(cx, p, v.danmaku.Get, v.mini)
	// subDir holds the downloaded subtitles while the page shows.
	var subDir string
	// cancel stops the open source's subtitle downloads; seq tells the
	// opens apart.
	cancel, seq := func() {}, 0

	// What was last seen of the open source, so saving reads no signal.
	var key string
	var pos, dur, saved time.Duration
	var ended bool
	// writes keeps the file's writes off the engine and in order.
	writes := make(chan func(), 16)
	go func() {
		for w := range writes {
			w()
		}
	}()
	save := func() {
		if key == "" || pos == saved {
			return
		}
		saved = pos
		k, at := key, pos
		if pg.OnPosition != nil {
			pg.OnPosition(k, at, dur)
		}
		if resumable(at, dur) {
			writes <- func() { pg.Positions.Set(k, at) }
		} else {
			writes <- func() { pg.Positions.Remove(k) }
		}
	}
	addSubtitles := func(subs []Subtitle) {
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		seq++
		n := seq
		if subDir == "" && len(subs) > 0 {
			subDir, _ = os.MkdirTemp(cx.CacheDir(), "subtitles-")
		}
		for i, sub := range subs {
			go func() {
				file, err := subtitleFile(ctx, client, subDir, i, sub)
				cx.Post(func() {
					if n != seq {
						return
					}
					if err == nil {
						err = p.AddSubtitles(file)
					}
					if err != nil {
						log.Printf("subtitles %s: %v", sub.Label, err)
					}
				})
			}()
		}
	}
	open := func(s Source) {
		save()
		cancel()
		key, pos, dur, saved, ended = "", 0, 0, 0, false
		offer.Set(0)
		started.Set(false)
		current.Set(&s)
		if s.Validate() != nil {
			invalid.Set(true)
			p.Close()
			v.dm.open(nil)
			return
		}
		invalid.Set(false)
		key = s.key()
		p.Open(s.URL, s.options()...)
		v.dm.open(s.Danmaku)
		addSubtitles(s.Subtitles)
		p.Play()
		if s.Start > 0 {
			p.Seek(s.Start, player.Exact)
		} else if s.FromStart {
		} else if at, ok := pg.Positions.Get(key); ok {
			offer.Set(at)
			cx.AfterFunc(offerFor, func() {
				if offer.GetUntracked() == at {
					offer.Set(0)
				}
			})
		}
	}
	cx.Effect(func() {
		s := pg.Source()
		cx.Untrack(func() { open(s) })
	})
	retry := func() {
		s := *current.GetUntracked()
		if pos > 0 {
			s.Start = pos
		}
		open(s)
	}
	cx.Effect(func() {
		at, state := p.Position(), p.State()
		cx.Untrack(func() {
			if state == player.Playing && !started.GetUntracked() {
				started.Set(true)
			}
			if key == "" || p.Info() == nil {
				return
			}
			pos, dur = at, p.Duration()
			if state == player.Ended {
				pos = dur
			}
			if state == player.Paused || state == player.Ended || state == player.Failed || (pos-saved).Abs() >= saveEvery {
				save()
			}
			if state == player.Ended && !ended {
				ended = true
				if pg.OnEnded != nil {
					pg.OnEnded()
				}
			}
		})
	})
	cx.Cleanup(func() {
		save()
		cancel()
		close(writes)
		if subDir != "" {
			os.RemoveAll(subDir)
		}
	})

	// shown keeps the bars up until the pointer rests or leaves; over is
	// the pointer over the controls, which keeps them.
	shown := core.NewSignal(cx, true)
	over := core.NewSignal(cx, false)
	stop := func() bool { return false }
	wake := func() {
		shown.Set(true)
		stop()
		stop = cx.AfterFunc(stillFor, func() { shown.Set(false) })
	}
	leave := func() {
		stop()
		shown.Set(false)
	}
	wake()
	bars := func() bool {
		return shown.Get() || over.Get() || v.menu.Get() != "" || !started.Get() || v.failed()
	}
	var before core.Size
	togglePin := func() { v.pin(!v.pinned.GetUntracked(), &before) }
	bindKeys(cx, v, togglePin)
	windowControls(cx, pg.Window)
	return core.View{
		TestID: "player",
		Ref:    v.at.page,
		ClassFunc: func() string {
			switch {
			case !pg.Window || v.fullScreen():
				return "absolute inset-0 overflow-hidden bg-black"
			case v.pinned.Get():
				return "absolute inset-0 overflow-hidden rounded-2xl border border-[#FF6B4A]/50 bg-black"
			}
			return "absolute inset-0 overflow-hidden rounded-2xl border border-white/15 bg-black"
		},
		StyleFunc: func() core.Style {
			if bars() {
				return core.Style{}
			}
			return core.Style{Cursor: core.CursorNone}
		},
		OnMouseEnter: func(core.MouseEvent) { wake() },
		OnMouseMove:  func(core.MouseEvent) { wake() },
		OnMouseLeave: func(core.MouseEvent) { leave() },
		OnLayout:     func(core.Bounds) { v.at.layout.Set(v.at.layout.GetUntracked() + 1) },
		Children: []core.Node{
			v.picture(bars),
			v.dm.layer(bars),
			// A menu open closes as the picture is pressed.
			core.Show{When: func() bool { return v.menu.Get() != "" }, Then: func(*core.Scope) core.Node {
				return core.View{Class: "absolute inset-0", Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOff}, OnPress: func(core.PressEvent) { v.menu.Set("") }}
			}},
			status(cx, p, invalid.Get, func() error {
				if invalid.Get() {
					return current.Get().Validate()
				}
				return p.Err()
			}, retry, pg.OnClose),
			core.View{
				Class: "absolute inset-0",
				StyleFunc: func() core.Style {
					st := core.Style{Opacity: core.Alpha(1), PointerEvents: core.PointerEventsBoxNone, Transition: core.Transition{Duration: fadeFor, Properties: core.TransitionOpacity}}
					if !bars() {
						st.Opacity, st.PointerEvents = core.Alpha(0), core.PointerEventsNone
					}
					return st
				},
				Children: []core.Node{
					v.topBar(togglePin),
					core.Show{
						When: func() bool { return !v.failed() },
						Then: func(cx *core.Scope) core.Node {
							return core.Group{v.controls(over), resumeOffer(cx, p, offer, v.at)}
						},
					},
				},
			},
			v.menus(),
		},
	}
}

// picture is the video, with its subtitles: in a window of its own, a
// handle that moves the window, and elsewhere pressed to play or pause.
func (v *view) picture(bars func() bool) core.Node {
	pv := player.View{
		Player:   v.p,
		TestID:   "video",
		Style:    core.Style{FlexGrow: 1, FlexShrink: 1, Background: core.RGB(0x000000)},
		Children: []core.Node{subtitles(v.p, bars)},
	}
	if !v.pg.Window {
		pv.Children = append(pv.Children, core.View{Class: "absolute inset-0", OnPress: func(core.PressEvent) { togglePlay(v.p) }})
		return pv
	}
	return core.View{
		Class:    "flex-1",
		Desktop:  core.DesktopViewProps{WindowDrag: core.WindowDragOn},
		Children: []core.Node{pv},
	}
}

// windowControls hides macOS's window buttons in a player window of its
// own, or else moves them down beside the close button while the page
// covers the window.
func windowControls(cx *core.Scope, window bool) {
	if !desktop.IsAvailable(cx) {
		return
	}
	if window {
		desktop.SetTitleBar(cx, desktop.TitleBarHidden, 0)
		desktop.SetWindowControls(cx, desktop.WindowControls{Hidden: true})
		return
	}
	desktop.SetWindowControls(cx, desktop.WindowControls{TrafficLights: core.Point{X: trafficLightsX, Y: trafficLightsY}})
	cx.Cleanup(func() { desktop.SetWindowControls(cx, desktop.WindowControls{}) })
}

// Where macOS's close button goes while the page covers the main window:
// its 14-point frame puts the buttons' centre on the close button's, 28
// points from the top.
const trafficLightsX, trafficLightsY = 18, 21

// bindKeys gives the page its keyboard shortcuts: Escape closes a menu,
// leaves full screen, then the page.
func bindKeys(cx *core.Scope, v *view, pin func()) {
	p := v.p
	seekBy := func(d time.Duration) {
		t := max(p.Position()+d, 0)
		if dur := p.Duration(); dur > 0 {
			t = min(t, dur)
		}
		p.Seek(t, player.Exact)
	}
	volumeBy := func(d float64) {
		p.SetMuted(false)
		p.SetVolume(min(max(p.Volume()+d, 0), 1))
	}
	repeat := core.KeyOptions{Repeat: true}
	cx.OnKey("space", func() { togglePlay(p) })
	cx.OnKeyWith("left", repeat, func() { seekBy(-seekStep) })
	cx.OnKeyWith("right", repeat, func() { seekBy(seekStep) })
	cx.OnKeyWith("up", repeat, func() { volumeBy(volumeStep) })
	cx.OnKeyWith("down", repeat, func() { volumeBy(-volumeStep) })
	cx.OnKey("f", func() { desktop.ToggleFullScreen(cx) })
	cx.OnKey("d", func() {
		if core.Untracked(cx, v.src).Danmaku != nil {
			v.setDanmaku(func(d *store.Danmaku) { d.Off = !d.Off })
		}
	})
	cx.OnKey("p", pin)
	cx.OnKey("escape", func() {
		switch {
		case v.menu.GetUntracked() != "":
			v.menu.Set("")
		case desktop.FullScreen(cx):
			desktop.ToggleFullScreen(cx)
		case v.pg.OnClose != nil:
			v.pg.OnClose()
		}
	})
}

func playing(p *player.Player) bool {
	s := p.State()
	return s == player.Playing || s == player.Buffering
}

func togglePlay(p *player.Player) {
	if playing(p) {
		p.Pause()
	} else {
		p.Play()
	}
}

// roundGlass is the look of the top bar's round buttons: a dark blur.
const roundGlass = "border border-white/20 bg-black/35 shadow-lg hover:bg-black/55"

// topBar is the bar over the picture's top: closing, where the source
// comes from and its title, and pinning the window, moving the window
// as the title bar it replaces does. Once the source failed, it darkens
// for the title to read.
func (v *view) topBar(pin func()) core.Node {
	cx := v.cx
	closeButton := darkButton(cx, "close", locales.Close(cx), "size-8 "+roundGlass, v.pg.OnClose, core.Icon{SVG: lucide.X(), Class: "size-4 text-white"})
	closeButton.Desktop = core.DesktopViewProps{WindowDrag: core.WindowDragOff}
	title := core.View{
		Class:     "max-w-[520px] flex-row items-center gap-2 rounded-full border border-white/15 bg-black/30 py-1 pr-3",
		StyleFunc: func() core.Style { return core.Style{FlexShrink: 1, MinWidth: core.Pt(0)} },
		ClassFunc: func() string {
			if v.src().Badge == nil {
				return "pl-3"
			}
			return "pl-1.5"
		},
		Children: []core.Node{
			core.Dynamic(func(*core.Scope) core.Node { return v.src().Badge }),
			core.Text{TestID: "title", ContentFunc: func() string {
				if v.mini() {
					return v.src().partTitle()
				}
				return v.src().title()
			}, MaxLines: 1, Class: "text-xs font-medium text-white/95", Style: core.Style{FlexShrink: 1}},
		},
	}
	children := []core.Node{closeButton, title, core.View{Class: "flex-1"}}
	if v.pg.Window {
		pinButton := labelled(darkButton(cx, "pin", "", "h-8 flex-row gap-1.5 px-2.5", pin,
			core.Icon{SVG: lucide.Pin(), Class: "size-3.5 text-white"},
			core.Show{When: v.pinned.Get, Then: func(cx *core.Scope) core.Node {
				return core.Text{Content: locales.Pinned(cx), Class: "text-xs font-semibold text-white"}
			}},
		), func() string {
			if v.pinned.Get() {
				return locales.Unpin(cx)
			}
			return locales.PinOnTop(cx)
		})
		pinButton.Desktop = core.DesktopViewProps{WindowDrag: core.WindowDragOff}
		pinButton.ClassFunc = func() string {
			if v.pinned.Get() {
				return "border border-[#FF8A6B]/60 bg-[#E8553A] px-3 shadow-lg hover:bg-[#d94a30]"
			}
			return roundGlass
		}
		children = append(children, core.Show{When: func() bool { return !v.fullScreen() }, Then: func(*core.Scope) core.Node { return pinButton }})
	}
	return core.View{
		Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOn},
		Class:   "absolute top-0 right-0 left-0 h-14 flex-row items-center gap-2.5 px-3.5",
		ClassFunc: func() string {
			if v.failed() {
				return "bg-linear-to-b from-black/70 to-transparent"
			}
			return ""
		},
		StyleFunc: func() core.Style {
			if v.pg.Window || !desktop.IsAvailable(cx) {
				return core.Style{}
			}
			// Over the main window, the window's buttons go before the
			// close button.
			return core.Style{Padding: core.Edges{Left: max(desktop.TitleBar(cx).Left+4, 14)}}
		},
		Children: children,
	}
}

// glass is Liquid Glass, untinted, where the system draws it, and
// elsewhere a translucent dark fill and rim over a blur of the picture,
// not NSGo's glass shader on Windows, which leaves clear glass unblurred.
func glass(cx *core.Scope, effect core.GlassEffect, rim bool) func() core.Style {
	return func() core.Style {
		st := core.Style{BackdropFilters: []core.Filter{core.Blur(40), core.Saturate(1.5)}}
		if systemGlass(cx) {
			st.GlassEffect = effect
		} else {
			st.Background = core.RGBA(0x000000, 0.4)
			if rim {
				st.BorderWidth, st.BorderColor = 1, core.RGBA(0xffffff, 0.2)
			}
		}
		return st
	}
}

// systemGlass reports whether the system draws Liquid Glass with its own
// rim, as Apple's do.
func systemGlass(cx *core.Scope) bool {
	switch cx.Env().OS() {
	case core.OSMacOS, core.OSIOS:
		return !cx.Env().Simulated()
	}
	return false
}

// anchors are where the floating controls lie in the page, so what
// opens from them sits above them.
type anchors struct {
	page, panel *core.Ref
	// layout changes as the page lays out again.
	layout *core.Signal[int]
}

// above places a view gap points above the controls, its right edge
// right points right of theirs, kept inside the page.
func (a *anchors) above(gap, right core.Points) func() core.Style {
	return func() core.Style {
		a.layout.Get()
		page, _ := a.page.Bounds()
		panel, ok := a.panel.Bounds()
		if !ok {
			return core.Style{Bottom: core.Pt(78), Right: core.Pt(14)}
		}
		bottom := page.Origin.Y + page.Size.Height - panel.Origin.Y + gap
		edge := page.Origin.X + page.Size.Width - (panel.Origin.X + panel.Size.Width + right)
		return core.Style{Bottom: core.Pt(bottom), Right: core.Pt(max(edge, 8))}
	}
}

// left places a view gap points above the controls, at their left edge.
func (a *anchors) left(gap core.Points) func() core.Style {
	return func() core.Style {
		a.layout.Get()
		page, _ := a.page.Bounds()
		panel, ok := a.panel.Bounds()
		if !ok {
			return core.Style{Bottom: core.Pt(80), Left: core.Pt(20)}
		}
		return core.Style{Bottom: core.Pt(page.Origin.Y + page.Size.Height - panel.Origin.Y + gap), Left: core.Pt(panel.Origin.X - page.Origin.X)}
	}
}

// darkButton is a control over the picture.
func darkButton(cx *core.Scope, testID, label, class string, press func(), children ...core.Node) core.View {
	return core.View{
		TestID: testID, Role: core.RoleButton, Label: label,
		OnPress:  func(core.PressEvent) { press() },
		Class:    "items-center justify-center rounded-full hover:bg-white/15 " + class,
		Children: children,
	}
}

// labelled names a control by what label reads.
func labelled(v core.View, label func() string) core.View {
	v.Label, v.LabelFunc = "", label
	return v
}

// subtitles draws the chosen track's cues over the picture, above the
// controls while they show.
func subtitles(p *player.Player, bars func() bool) core.Node {
	return core.View{
		Class: "absolute right-0 left-0 items-center px-10",
		StyleFunc: func() core.Style {
			if bars() {
				return core.Style{Bottom: core.Pt(84)}
			}
			return core.Style{Bottom: core.Pt(32)}
		},
		PointerEvents: core.PointerEventsNone,
		Children: []core.Node{core.Text{
			TestID: "cue",
			ContentFunc: func() string {
				var lines []string
				for _, c := range p.Cues() {
					lines = append(lines, c.Text)
				}
				return strings.Join(lines, "\n")
			},
			Class: "text-center text-[19px] font-medium text-white",
			Style: core.Style{TextShadowColor: core.RGBA(0x000000, 0.95), TextShadowRadius: 3},
		}},
	}
}

// status shows why nothing plays: opening or buffering, the last picture
// dimmed, or a failure, with closing and trying again.
func status(cx *core.Scope, p *player.Player, invalid func() bool, cause func() error, retry, back func()) core.Node {
	failed := func() bool { return invalid() || p.State() == player.Failed }
	return core.Group{
		core.Show{
			When: func() bool { return !failed() && (p.State() == player.Buffering || p.State() == player.Opening) },
			Then: func(*core.Scope) core.Node {
				return core.View{TestID: "loading", Class: "absolute inset-0 items-center justify-center bg-black/60", PointerEvents: core.PointerEventsNone, Children: []core.Node{
					core.View{Class: "size-14 items-center justify-center rounded-full shadow-2xl", StyleFunc: glass(cx, core.GlassClear, true), Children: []core.Node{
						ui.Spinner{Size: 24, Tint: func() core.Color { return core.RGB(0xffffff) }},
					}},
				}}
			},
		},
		core.Show{
			When: failed,
			Then: func(cx *core.Scope) core.Node {
				children := []core.Node{
					core.View{Class: "size-12 items-center justify-center rounded-2xl bg-white/10", Children: []core.Node{
						core.Icon{SVG: lucide.CircleAlert(), Class: "size-5 text-rose-400"},
					}},
					core.Text{TestID: "error", Content: locales.CannotPlay(cx), Class: "text-center text-sm font-semibold text-white"},
					core.Text{TestID: "error-detail", ContentFunc: func() string {
						if err := cause(); err != nil {
							return err.Error()
						}
						return ""
					}, Class: "max-w-[440px] text-center text-xs text-white/50"},
					core.View{Class: "mt-2 flex-row gap-2", Children: []core.Node{
						core.View{TestID: "error-back", Role: core.RoleButton, Label: locales.Close(cx), OnPress: func(core.PressEvent) { back() },
							Class:    "h-8 items-center justify-center rounded-lg bg-white/10 px-3.5 hover:bg-white/20",
							Children: []core.Node{core.Text{Content: locales.Close(cx), Class: "text-xs font-medium text-white"}}},
						core.View{TestID: "error-retry", Role: core.RoleButton, Label: locales.Retry(cx), OnPress: func(core.PressEvent) { retry() },
							Class:    "h-8 items-center justify-center rounded-lg bg-white px-3.5",
							Children: []core.Node{core.Text{Content: locales.Retry(cx), Class: "text-xs font-medium text-zinc-900"}}},
					}},
				}
				return core.View{Class: "absolute inset-0 items-center justify-center gap-3 bg-black px-10", Children: children}
			},
		},
	}
}

// resumeOffer offers to jump to where the video was left, above the
// controls' left end.
func resumeOffer(cx *core.Scope, p *player.Player, offer *core.Signal[time.Duration], an *anchors) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		at := offer.Get()
		if at == 0 {
			return nil
		}
		return core.Show{
			When: func() bool { return p.State() != player.Failed },
			Then: func(cx *core.Scope) core.Node {
				text := locales.ResumeAt(cx, clock(at))
				place, look := an.left(12), glass(cx, core.GlassClear, true)
				return core.View{
					Class:     "absolute flex-row items-center gap-1 rounded-full py-1 pr-1 pl-3 shadow-lg",
					StyleFunc: func() core.Style { return look().Merge(place()) },
					Children: []core.Node{
						core.View{
							TestID: "resume", Role: core.RoleButton, Label: text,
							OnPress: func(core.PressEvent) {
								p.Seek(at, player.Exact)
								offer.Set(0)
							},
							Class: "flex-row items-center gap-1",
							Children: []core.Node{
								core.Icon{SVG: lucide.RotateCcw(), Class: "size-3.5 text-white"},
								core.Text{Content: text, Class: "px-1 text-xs font-medium text-white"},
							},
						},
						core.View{
							TestID: "dismiss-resume", Role: core.RoleButton, Label: locales.Dismiss(cx), OnPress: func(core.PressEvent) { offer.Set(0) },
							Class:    "size-6 items-center justify-center rounded-full hover:bg-white/15",
							Children: []core.Node{core.Icon{SVG: lucide.X(), Class: "size-3 text-white/80"}},
						},
					},
				}
			},
		}
	})
}
