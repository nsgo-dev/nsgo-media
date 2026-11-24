package playback

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"strconv"

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
	// offerFor is how long the resume offer shows, and stillFor how long
	// the pointer rests before the bars hide while playing.
	offerFor = 10 * time.Second
	stillFor = 3 * time.Second
)

// Page plays a source with the app's controls over the whole window,
// reopening as Source changes, and keeps its resume position in
// Positions.
type Page struct {
	Source    func() Source
	Positions *store.Positions
	OnClose   func()
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

// Render implements core.Node.
func (pg Page) Render(cx *core.Scope) core.Node {
	p := player.New(cx)
	invalid := core.NewSignal(cx, false)
	title := core.NewSignal(cx, "")
	// offer is the stored position the viewer may resume at, 0 for none.
	offer := core.NewSignal(cx, time.Duration(0))
	current := core.NewSignal(cx, &Source{})
	client := pg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
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
		title.Set(s.title())
		current.Set(&s)
		if s.Validate() != nil {
			invalid.Set(true)
			p.Close()
			return
		}
		invalid.Set(false)
		key = s.key()
		p.Open(s.URL, s.options()...)
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
	retry := func() { open(*current.GetUntracked()) }
	cx.Effect(func() {
		at, state := p.Position(), p.State()
		cx.Untrack(func() {
			if key == "" || p.Info() == nil {
				return
			}
			pos, dur = at, p.Duration()
			if state == player.Ended {
				pos = dur
			}
			if state == player.Paused || state == player.Ended || (pos-saved).Abs() >= saveEvery {
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

	// menu is the menu open over the picture, "" for none.
	menu := core.NewSignal(cx, "")
	// shown keeps the bars up; still hides them once the pointer rests.
	shown := core.NewSignal(cx, true)
	stop := func() bool { return false }
	wake := func() {
		shown.Set(true)
		stop()
		stop = cx.AfterFunc(stillFor, func() { shown.Set(false) })
	}
	wake()
	bars := func() bool {
		return shown.Get() || menu.Get() != "" || !playing(p) || invalid.Get() || p.State() == player.Failed
	}
	bindKeys(cx, p, menu, pg.OnClose)
	placeTrafficLights(cx)
	src := func() Source { return *current.Get() }
	failed := func() bool { return invalid.Get() || p.State() == player.Failed }
	at := &anchors{page: &core.Ref{}, panel: &core.Ref{}, buttons: map[string]*core.Ref{}, layout: core.NewSignal(cx, 0)}
	return core.View{
		TestID:      "player",
		Ref:         at.page,
		Class:       "absolute inset-0 overflow-hidden bg-black",
		OnMouseMove: func(core.MouseEvent) { wake() },
		OnLayout:    func(core.Bounds) { at.layout.Set(at.layout.GetUntracked() + 1) },
		Children: []core.Node{
			player.View{
				Player: p,
				TestID: "video",
				Style:  core.Style{FlexGrow: 1, FlexShrink: 1, Background: core.RGB(0x000000)},
				Children: []core.Node{
					subtitles(p, bars),
					core.View{Class: "absolute inset-0", OnPress: func(core.PressEvent) {
						if menu.GetUntracked() != "" {
							menu.Set("")
							return
						}
						togglePlay(p)
					}},
				},
			},
			status(cx, p, invalid.Get, func() error {
				if invalid.Get() {
					return current.Get().Validate()
				}
				return p.Err()
			}, retry, pg.OnClose),
			core.Show{
				When: bars,
				Then: func(cx *core.Scope) core.Node {
					return core.Group{
						header(cx, title.Get, failed, pg.OnClose),
						core.Show{
							When: func() bool { return !failed() },
							Then: func(cx *core.Scope) core.Node {
								return core.Group{controls(cx, p, src, menu, pg, at), resumeOffer(cx, p, offer, at)}
							},
						},
					}
				},
			},
			menus(cx, p, src, menu, pg, at),
		},
	}
}

// bindKeys gives the page its keyboard shortcuts: Escape closes a menu,
// leaves full screen, then the page.
func bindKeys(cx *core.Scope, p *player.Player, menu *core.Signal[string], close func()) {
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
	cx.OnKey("escape", func() {
		switch {
		case menu.GetUntracked() != "":
			menu.Set("")
		case desktop.FullScreen(cx):
			desktop.ToggleFullScreen(cx)
		case close != nil:
			close()
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

// header is the page's top bar over the picture: a round back button
// beside the window's buttons, and the source's title once it failed,
// moving the window as the title bar it replaces does.
func header(cx *core.Scope, title func() string, titled func() bool, onClose func()) core.Node {
	back := darkButton(cx, "back", locales.Back(cx), "size-9 rounded-full", onClose, core.Icon{SVG: lucide.ChevronLeft(), Class: "size-[18px] text-white"})
	back.StyleFunc = glass(cx, core.GlassClear, true)
	return core.View{
		Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOn},
		Class:   "absolute top-0 right-0 left-0 h-[76px] flex-row items-center gap-3",
		ClassFunc: func() string {
			if titled() {
				return "bg-linear-to-b from-black/70 to-transparent"
			}
			return ""
		},
		StyleFunc: func() core.Style {
			// The window's buttons end 8 points before TitleBar's Left.
			return core.Style{Padding: core.Edges{Left: max(desktop.TitleBar(cx).Left+8, 20), Right: 20}}
		},
		Children: []core.Node{
			back,
			core.Show{When: titled, Then: func(*core.Scope) core.Node {
				return core.Text{TestID: "title", ContentFunc: title, MaxLines: 1, Class: "flex-1 text-sm font-semibold text-white"}
			}},
		},
	}
}

// Where macOS's close button goes while the page shows: its 14-point
// frame puts the buttons' centre on the back button's, 38 points from
// the top, and their dots 20 points in.
const trafficLightsX, trafficLightsY = 19, 31

// placeTrafficLights moves macOS's window buttons down beside the back
// button while the page shows.
func placeTrafficLights(cx *core.Scope) {
	if !desktop.IsAvailable(cx) {
		return
	}
	desktop.SetWindowControls(cx, desktop.WindowControls{TrafficLights: core.Point{X: trafficLightsX, Y: trafficLightsY}})
	cx.Cleanup(func() { desktop.SetWindowControls(cx, desktop.WindowControls{}) })
}

// glass is Liquid Glass, untinted, where the system draws it, and
// elsewhere a translucent white fill and rim over a blur of the picture.
func glass(cx *core.Scope, effect core.GlassEffect, rim bool) func() core.Style {
	return func() core.Style {
		st := core.Style{BackdropFilters: []core.Filter{core.Blur(40), core.Saturate(1.5)}, GlassEffect: effect}
		if !systemGlass(cx) {
			st.Background = core.RGBA(0xffffff, 0.1)
			if rim {
				st.BorderWidth, st.BorderColor = 1, core.RGBA(0xffffff, 0.25)
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
	buttons     map[string]*core.Ref
	// layout changes as the page lays out again.
	layout *core.Signal[int]
}

// ref is the ref of the button named name.
func (a *anchors) ref(name string) *core.Ref {
	r := a.buttons[name]
	if r == nil {
		r = &core.Ref{}
		a.buttons[name] = r
	}
	return r
}

// above places a view 12 points above the panel, its right edge on the
// named button's, or its left on the panel's for "".
func (a *anchors) above(name string) func() core.Style {
	return func() core.Style {
		a.layout.Get()
		page, _ := a.page.Bounds()
		panel, ok := a.panel.Bounds()
		if !ok {
			return core.Style{Bottom: core.Pt(136), Left: core.Pt(20)}
		}
		st := core.Style{Bottom: core.Pt(page.Origin.Y + page.Size.Height - panel.Origin.Y + 12)}
		if name == "" {
			st.Left = core.Pt(panel.Origin.X - page.Origin.X)
			return st
		}
		if b, ok := a.ref(name).Bounds(); ok {
			st.Right = core.Pt(max(page.Origin.X+page.Size.Width-b.Origin.X-b.Size.Width, 8))
		}
		return st
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
// bars while they show.
func subtitles(p *player.Player, bars func() bool) core.Node {
	return core.View{
		Class: "absolute right-0 left-0 items-center px-10",
		StyleFunc: func() core.Style {
			if bars() {
				return core.Style{Bottom: core.Pt(150)}
			}
			return core.Style{Bottom: core.Pt(40)}
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
			Class: "text-center text-[22px] font-medium text-white",
			Style: core.Style{TextShadowColor: core.RGBA(0x000000, 0.9), TextShadowRadius: 3},
		}},
	}
}

// status shows why nothing plays: opening or buffering, the last picture
// dimmed, or a failure, with going back and trying again.
func status(cx *core.Scope, p *player.Player, invalid func() bool, cause func() error, retry, back func()) core.Node {
	failed := func() bool { return invalid() || p.State() == player.Failed }
	return core.Group{
		core.Show{
			When: func() bool { return !failed() && (p.State() == player.Buffering || p.State() == player.Opening) },
			Then: func(*core.Scope) core.Node {
				return core.View{TestID: "loading", Class: "absolute inset-0 items-center justify-center bg-black/60", PointerEvents: core.PointerEventsNone, Children: []core.Node{
					core.View{Class: "size-16 items-center justify-center rounded-full shadow-2xl", StyleFunc: glass(cx, core.GlassClear, true), Children: []core.Node{
						ui.Spinner{Size: 28, Tint: func() core.Color { return core.RGB(0xffffff) }},
					}},
				}}
			},
		},
		core.Show{
			When: failed,
			Then: func(cx *core.Scope) core.Node {
				children := []core.Node{
					core.View{Class: "size-14 items-center justify-center rounded-2xl bg-white/10", Children: []core.Node{
						core.Icon{SVG: lucide.CircleAlert(), Class: "size-6 text-rose-400"},
					}},
					core.Text{TestID: "error", Content: locales.CannotPlay(cx), Class: "text-center text-[15px] font-semibold text-white"},
					core.Text{TestID: "error-detail", ContentFunc: func() string {
						if err := cause(); err != nil {
							return err.Error()
						}
						return ""
					}, Class: "max-w-[480px] text-center text-[13px] text-white/50"},
					core.View{Class: "mt-2 flex-row gap-2", Children: []core.Node{
						core.View{TestID: "error-back", Role: core.RoleButton, Label: locales.Back(cx), OnPress: func(core.PressEvent) { back() },
							Class:    "h-8 items-center justify-center rounded-lg bg-white/10 px-3.5 hover:bg-white/20",
							Children: []core.Node{core.Text{Content: locales.Back(cx), Class: "text-[13px] font-medium text-white"}}},
						core.View{TestID: "error-retry", Role: core.RoleButton, Label: locales.Retry(cx), OnPress: func(core.PressEvent) { retry() },
							Class:    "h-8 items-center justify-center rounded-lg bg-white px-3.5",
							Children: []core.Node{core.Text{Content: locales.Retry(cx), Class: "text-[13px] font-medium text-zinc-900"}}},
					}},
				}
				return core.View{Class: "absolute inset-0 items-center justify-center gap-3 bg-black px-10", Children: children}
			},
		},
	}
}

// resumeOffer offers to jump to where the video was left.
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
				place, look := an.above(""), glass(cx, core.GlassClear, true)
				return core.View{
					Class:     "absolute flex-row items-center gap-1 rounded-full py-1 pr-1 pl-3.5 shadow-lg",
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
								core.Icon{SVG: lucide.RotateCcw(), Class: "size-4 text-white"},
								core.Text{Content: text, Class: "px-1 text-[13px] font-medium text-white"},
							},
						},
						core.View{
							TestID: "dismiss-resume", Role: core.RoleButton, Label: locales.Dismiss(cx), OnPress: func(core.PressEvent) { offer.Set(0) },
							Class:    "size-7 items-center justify-center rounded-full hover:bg-white/15",
							Children: []core.Node{core.Icon{SVG: lucide.X(), Class: "size-3.5 text-white/80"}},
						},
					},
				}
			},
		}
	})
}

// track is a bar the pointer sets a share of, as the seek bar and the
// volume are: pressed or dragged, set hears the share there. A seek bar
// shows what is buffered and a thumb.
func track(testID, label, class, bar string, share, buffered func() float64, set func(float64), seekBar bool) core.Node {
	width := core.Points(1)
	at := func(x core.Points) float64 { return float64(min(max(x/width, 0), 1)) }
	pct := func(f float64) core.Length { return core.Pct(float32(min(max(f, 0), 1) * 100)) }
	down := false
	fill := []core.Node{}
	if buffered != nil {
		fill = append(fill, core.View{Class: "absolute top-0 bottom-0 left-0 bg-white/30", StyleFunc: func() core.Style { return core.Style{Width: pct(buffered())} }})
	}
	fill = append(fill, core.View{Class: "absolute top-0 bottom-0 left-0 rounded-full bg-white", StyleFunc: func() core.Style { return core.Style{Width: pct(share())} }})
	children := []core.Node{core.View{Class: "overflow-hidden rounded-full " + bar, Children: fill}}
	if seekBar {
		children = append(children, core.View{Class: "absolute -ml-2 size-4 rounded-full bg-white shadow-lg", PointerEvents: core.PointerEventsNone,
			StyleFunc: func() core.Style { return core.Style{Left: pct(share())} }})
	}
	return core.View{
		TestID: testID, Label: label, Role: core.RoleAdjustable,
		Class:    "h-4 justify-center " + class,
		OnLayout: func(b core.Bounds) { width = max(b.Size.Width, 1) },
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
		Children:  children,
	}
}

// controls are the floating panel over the picture's bottom: playing and
// the next part, the volume, the title, then the parts, speed, tracks,
// quality and full screen, over the seek bar between the time played and
// the time left.
func controls(cx *core.Scope, p *player.Player, src func() Source, menu *core.Signal[string], pg Page, at *anchors) core.Node {
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
	// opener is a button that opens a menu above it, lit while it is open.
	opener := func(name, testID, label, class string, children ...core.Node) core.View {
		v := darkButton(cx, testID, label, class, toggle(name), children...)
		v.Ref = at.ref(name)
		v.ClassFunc = func() string {
			if menu.Get() == name {
				return "bg-white/25"
			}
			return ""
		}
		return v
	}
	// Where the system draws Liquid Glass, nothing tints it; elsewhere the
	// groups of buttons are lighter capsules on the panel.
	tint := " bg-white/10"
	if systemGlass(cx) {
		tint = ""
	}
	capsule := func(children ...core.Node) core.Node {
		return core.View{Class: "flex-row items-center gap-0.5 rounded-full p-1" + tint, Children: children}
	}
	label := "text-[13px] font-semibold text-white"
	timeStyle := core.Style{FontVariant: core.FontTabularNums}
	hasParts := func() bool { return len(src().Parts) > 1 && pg.OnPart != nil }
	hasNext := func() bool { s := src(); return hasParts() && s.Part+1 < len(s.Parts) }
	hasQualities := func() bool { return pg.OnQuality != nil && len(src().Qualities) > 1 }
	hasTracks := func() bool { return countTracks(p, player.Subtitle) > 0 || countTracks(p, player.Audio) > 1 }
	when := func(cond func() bool, n core.Node) core.Node {
		return core.Show{When: cond, Then: func(*core.Scope) core.Node { return n }}
	}
	// caption is the part's title over the video's name, else the title.
	caption := func() string {
		if s := src(); len(s.Parts) > 1 && s.Video != "" {
			return s.Parts[min(s.Part, len(s.Parts)-1)].Title
		}
		return src().title()
	}
	subcaption := func() string {
		s := src()
		if len(s.Parts) <= 1 || s.Video == "" {
			return ""
		}
		return s.Parts[min(s.Part, len(s.Parts)-1)].Label + " · " + s.Video
	}
	// total shows the duration in place of the time left.
	total := core.NewSignal(cx, false)
	play := labelled(darkButton(cx, "play", "", "size-10 bg-white/90 shadow-md hover:bg-white", func() { togglePlay(p) }, core.Icon{SVGFunc: func() *core.SVG {
		if playing(p) {
			return lucide.Pause()
		}
		return lucide.Play()
	}, Class: "size-5 text-zinc-900"}), func() string {
		if playing(p) {
			return locales.Pause(cx)
		}
		return locales.Play(cx)
	})
	top := core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
		capsule(play, when(hasNext, darkButton(cx, "next", locales.NextPart(cx), "size-10", func() { pg.OnPart(src().Part + 1) },
			core.Icon{SVG: lucide.SkipForward(), Class: "size-[18px] text-white"}))),
		core.View{Class: "h-12 flex-row items-center gap-2 rounded-full pr-4 pl-1" + tint, Children: []core.Node{
			labelled(darkButton(cx, "mute", "", "size-10", func() { p.SetMuted(!p.Muted()) }, core.Icon{SVGFunc: func() *core.SVG {
				if p.Muted() || p.Volume() == 0 {
					return lucide.VolumeX()
				}
				return lucide.Volume2()
			}, Class: "size-[18px] text-white"}), func() string {
				if p.Muted() {
					return locales.Unmute(cx)
				}
				return locales.Mute(cx)
			}),
			track("volume", locales.Volume(cx), "w-20", "h-1 bg-white/25", func() float64 {
				if p.Muted() {
					return 0
				}
				return p.Volume()
			}, nil, func(v float64) {
				p.SetMuted(false)
				p.SetVolume(v)
			}, false),
		}},
		core.View{Class: "flex-1 items-center gap-0.5 px-4", Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
			core.Text{TestID: "title", ContentFunc: caption, MaxLines: 1, Class: "text-[13px] font-semibold text-white"},
			when(func() bool { return subcaption() != "" }, core.Text{TestID: "subtitle", ContentFunc: subcaption, MaxLines: 1, Class: "text-[11px] font-medium text-white/60"}),
		}},
		capsule(
			when(hasParts, opener("parts", "parts", locales.Parts(cx), "h-10 flex-row gap-1.5 px-3",
				core.Icon{SVG: lucide.ListVideo(), Class: "size-[18px] text-white"},
				core.Text{ContentFunc: func() string {
					s := src()
					return fmt.Sprintf("%s/%d", s.Parts[min(s.Part, len(s.Parts)-1)].Label, len(s.Parts))
				}, Class: "text-[13px] font-medium text-white"})),
			opener("speed", "rate", locales.Speed(cx), "h-10 min-w-10 px-2.5",
				core.Text{ContentFunc: func() string { return rateLabel(p.Rate()) }, Class: label}),
			when(hasTracks, opener("tracks", "tracks", locales.SubtitlesAndAudio(cx), "size-10",
				core.Icon{SVG: lucide.Captions(), Class: "size-5 text-white"})),
			when(hasQualities, opener("quality", "quality", locales.Quality(cx), "h-10 px-3",
				core.Text{ContentFunc: func() string { return qualityLabel(src()) }, Class: label})),
			labelled(darkButton(cx, "fullscreen", "", "size-10", func() { desktop.ToggleFullScreen(cx) }, core.Icon{SVGFunc: func() *core.SVG {
				if desktop.FullScreen(cx) {
					return lucide.Minimize()
				}
				return lucide.Maximize()
			}, Class: "size-[18px] text-white"}), func() string {
				if desktop.FullScreen(cx) {
					return locales.ExitFullScreen(cx)
				}
				return locales.FullScreen(cx)
			}),
		),
	}}
	bottom := core.View{Class: "flex-row items-center gap-3 px-1", Children: []core.Node{
		core.Text{TestID: "position", ContentFunc: func() string { return clock(p.Position()) }, Class: "w-14 text-[12px] font-medium text-white/90", Style: timeStyle},
		track("seek", locales.Position(cx), "flex-1", "h-1.5 bg-white/20", share, buffered, seek, true),
		core.Text{TestID: "duration", ContentFunc: func() string {
			if total.Get() {
				return clock(p.Duration())
			}
			return "−" + clock(max(p.Duration()-p.Position(), 0))
		}, OnPress: func(core.PressEvent) { total.Set(!total.GetUntracked()) },
			Class: "w-14 text-right text-[12px] font-medium text-white/60", Style: timeStyle},
	}}
	look := glass(cx, core.GlassClear, true)
	return core.View{
		Class: "absolute right-0 left-0 items-center px-5",
		StyleFunc: func() core.Style {
			if desktop.FullScreen(cx) {
				return core.Style{Bottom: core.Pt(32)}
			}
			return core.Style{Bottom: core.Pt(20)}
		},
		// The space beside the panel lets presses through to the picture.
		PointerEvents: core.PointerEventsBoxNone,
		Children: []core.Node{core.View{
			TestID: "controls", Ref: at.panel,
			Class:     "w-[800px] max-w-full gap-3 rounded-[28px] px-4 pt-3 pb-4 shadow-2xl",
			StyleFunc: look,
			Children: []core.Node{
				// The light along the panel's top, which Liquid Glass draws
				// itself.
				core.Show{When: func() bool { return !systemGlass(cx) }, Then: func(*core.Scope) core.Node {
					return core.View{Class: "absolute top-0 right-0 left-0 h-12 rounded-t-[28px] bg-linear-to-b from-white/20 to-transparent", PointerEvents: core.PointerEventsNone}
				}},
				top, bottom,
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

// menus are the panels the controls open over the picture, closing as
// one is chosen or the picture is pressed.
func menus(cx *core.Scope, p *player.Player, src func() Source, menu *core.Signal[string], pg Page, at *anchors) core.Node {
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
			return panel(cx, at.above("speed"), "w-32", locales.Speed(cx), rows...)
		case "quality":
			var rows []core.Node
			for i, q := range src().Qualities {
				rows = append(rows, choice(fmt.Sprintf("quality-%d", i), q.Label, func() bool { return src().Quality == q.ID }, func() {
					menu.Set("")
					if q.ID != src().Quality && pg.OnQuality != nil {
						pg.OnQuality(q.ID, p.Position())
					}
				}))
			}
			return panel(cx, at.above("quality"), "w-44", locales.Quality(cx), rows...)
		case "tracks":
			return tracksPanel(cx, p, at)
		case "parts":
			return partsPanel(cx, src, menu, pg, at)
		}
		return nil
	})
}

// menuGlass is the look of a menu over the picture: regular glass,
// denser than the controls' for reading, or a dark fill over a blur
// where the system draws no Liquid Glass.
func menuGlass(cx *core.Scope, place func() core.Style) func() core.Style {
	return func() core.Style {
		st := core.Style{BackdropFilters: []core.Filter{core.Blur(40), core.Saturate(1.5)}, GlassEffect: core.GlassRegular}
		if !systemGlass(cx) {
			st.Background = core.RGBA(0x18181b, 0.5)
			st.BorderWidth, st.BorderColor = 1, core.RGBA(0xffffff, 0.2)
		}
		return st.Merge(place())
	}
}

// panel is a menu over the picture, above the button that opened it.
func panel(cx *core.Scope, place func() core.Style, class, title string, rows ...core.Node) core.Node {
	return core.View{
		TestID:    "menu",
		Class:     "absolute gap-0.5 rounded-[20px] p-1.5 shadow-2xl " + class,
		StyleFunc: menuGlass(cx, place),
		Children: append([]core.Node{
			core.Text{Content: title, Class: "px-2.5 pt-1 pb-1.5 text-[11px] font-semibold text-white/50"},
		}, rows...),
	}
}

// choice is a row of a menu over the picture, checked while it is the
// one chosen.
func choice(testID, label string, checked func() bool, choose func()) core.Node {
	return core.View{
		TestID: testID, Role: core.RoleMenuItemRadio, Label: label, Selected: checked,
		OnPress: func(core.PressEvent) { choose() },
		Class:   "group h-8 flex-row items-center gap-2 rounded-xl px-2.5 hover:bg-white/10 selected:bg-white/15",
		Children: []core.Node{
			core.Text{Content: label, MaxLines: 1, Class: "flex-1 text-[13px] text-white/90 group-selected:font-medium group-selected:text-white"},
			core.Show{When: checked, Then: func(*core.Scope) core.Node {
				return core.Icon{SVG: lucide.Check(), Class: "size-4 text-white"}
			}},
		},
	}
}

// tracksPanel chooses the subtitle and audio tracks, each by its title
// or else its number.
func tracksPanel(cx *core.Scope, p *player.Player, at *anchors) core.Node {
	column := func(title string, rows []core.Node) core.Node {
		return core.View{Class: "flex-1 gap-0.5", Children: append([]core.Node{
			core.Text{Content: title, Class: "px-2.5 pt-1 pb-1.5 text-[11px] font-semibold text-white/50"},
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
		Class:     "absolute w-[380px] flex-row gap-1 rounded-[20px] p-1.5 shadow-2xl",
		StyleFunc: menuGlass(cx, at.above("tracks")),
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

// partsPanel lists the video's parts beside the picture; choosing one
// plays it.
func partsPanel(cx *core.Scope, src func() Source, menu *core.Signal[string], pg Page, at *anchors) core.Node {
	s := src()
	var rows []core.Node
	for i, pt := range s.Parts {
		current := i == s.Part
		length := ""
		if pt.Duration > 0 {
			length = clock(pt.Duration)
		}
		label, title, sub := "w-8 text-xs font-semibold text-white/50", "text-[13px] text-white/90", "text-xs text-white/40"
		box := "flex-row items-center gap-3 rounded-2xl px-2.5 py-2 hover:bg-white/10"
		if current {
			label, title, sub = "w-8 text-xs font-semibold text-white", "text-[13px] font-medium text-white", "text-xs text-white/60"
			box += " bg-white/15"
		}
		text := []core.Node{core.Text{Content: pt.Title, MaxLines: 1, Class: title}}
		if length != "" {
			text = append(text, core.Text{Content: length, Class: sub})
		}
		children := []core.Node{
			core.Text{Content: pt.Label, Class: label},
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: text},
		}
		if current {
			children = append(children, core.Icon{SVG: lucide.AudioLines(), Class: "size-4 text-white"})
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
	// The parts run down the picture's right, from its top to above the
	// controls.
	place := func() core.Style {
		return core.Style{Bottom: at.above("")().Bottom}
	}
	return core.View{
		TestID:    "menu",
		Class:     "absolute top-4 right-5 w-[300px] gap-1 rounded-[24px] p-2 shadow-2xl",
		StyleFunc: menuGlass(cx, place),
		Children: []core.Node{
			core.Text{Content: locales.PartsN(cx, len(s.Parts)), Class: "px-2.5 pt-1.5 pb-2 text-[13px] font-semibold text-white"},
			ui.ScrollView{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, ContentStyle: core.Style{Gap: 4}, Children: rows},
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
