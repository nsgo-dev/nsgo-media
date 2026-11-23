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
	src := func() Source { return *current.Get() }
	return core.View{
		TestID:      "player",
		Class:       "absolute inset-0 overflow-hidden bg-black",
		OnMouseMove: func(core.MouseEvent) { wake() },
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
						header(cx, title.Get, pg.OnClose),
						core.Show{
							When: func() bool { return !invalid.Get() && p.State() != player.Failed },
							Then: func(cx *core.Scope) core.Node {
								return core.Group{controls(cx, p, src, menu, pg), resumeOffer(cx, p, offer)}
							},
						},
					}
				},
			},
			menus(cx, p, src, menu, pg),
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

// header is the page's top bar over the picture: back and the source's
// title, clear of the window's buttons and moving the window as the
// title bar it replaces does.
func header(cx *core.Scope, title func() string, onClose func()) core.Node {
	return core.View{
		Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOn},
		Class:   "absolute top-0 right-0 left-0 h-[64px] flex-row items-center gap-3 bg-linear-to-b from-black/70 to-transparent pr-5",
		StyleFunc: func() core.Style {
			return core.Style{Padding: core.Edges{Left: max(desktop.TitleBar(cx).Left+12, 20), Right: 20}}
		},
		Children: []core.Node{
			darkButton(cx, "back", locales.Back(cx), "size-8 bg-white/10 hover:bg-white/20", onClose, core.Icon{SVG: lucide.ChevronLeft(), Class: "size-[18px] text-white"}),
			core.Text{TestID: "title", ContentFunc: title, MaxLines: 1, Class: "flex-1 text-sm font-semibold text-white"},
		},
	}
}

// darkButton is a control over the picture.
func darkButton(cx *core.Scope, testID, label, class string, press func(), children ...core.Node) core.View {
	return core.View{
		TestID: testID, Role: core.RoleButton, Label: label,
		OnPress:  func(core.PressEvent) { press() },
		Class:    "items-center justify-center rounded-lg hover:bg-white/10 " + class,
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
					core.View{Class: "size-14 items-center justify-center rounded-full bg-black/50", Children: []core.Node{
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
func resumeOffer(cx *core.Scope, p *player.Player, offer *core.Signal[time.Duration]) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		at := offer.Get()
		if at == 0 {
			return nil
		}
		return core.Show{
			When: func() bool { return p.State() != player.Failed },
			Then: func(cx *core.Scope) core.Node {
				text := locales.ResumeAt(cx, clock(at))
				return core.View{
					Class: "absolute bottom-[104px] left-5 flex-row items-center gap-1 rounded-full bg-black/60 py-1 pr-1 pl-3.5 shadow-lg",
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

// track is a thin bar the pointer sets a share of, as the seek bar and
// the volume are: pressed or dragged, set hears the share there.
func track(testID, label, class string, share, buffered func() float64, set func(float64), thumb bool) core.Node {
	width := core.Points(1)
	at := func(x core.Points) float64 { return float64(min(max(x/width, 0), 1)) }
	down := false
	children := []core.Node{core.View{Class: "h-1 overflow-hidden rounded-full bg-white/25", Children: []core.Node{
		core.View{Class: "absolute top-0 bottom-0 left-0 bg-white/35", StyleFunc: func() core.Style {
			if buffered == nil {
				return core.Style{Width: core.Pt(0)}
			}
			return core.Style{Width: core.Pct(float32(min(max(buffered(), 0), 1) * 100))}
		}},
		core.View{Class: "absolute top-0 bottom-0 left-0 rounded-full", ClassFunc: func() string {
			if thumb {
				return "bg-accent"
			}
			return "bg-white"
		}, StyleFunc: func() core.Style { return core.Style{Width: core.Pct(float32(min(max(share(), 0), 1) * 100))} }},
	}}}
	if thumb {
		children = append(children, core.View{Class: "absolute -ml-1.5 size-3 rounded-full bg-white shadow-md", PointerEvents: core.PointerEventsNone,
			StyleFunc: func() core.Style { return core.Style{Left: core.Pct(float32(min(max(share(), 0), 1) * 100))} }})
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

// controls are the bars at the bottom: the seek bar, then playing,
// the next part, the volume and the time, and at the end the parts,
// speed, tracks, quality and full screen.
func controls(cx *core.Scope, p *player.Player, src func() Source, menu *core.Signal[string], pg Page) core.Node {
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
	open := func(name, class string) func() string {
		return func() string {
			if menu.Get() == name {
				return class + " bg-white/15"
			}
			return class
		}
	}
	white := "text-[13px] font-medium text-white"
	timeStyle := core.Style{FontVariant: core.FontTabularNums}
	hasParts := func() bool { return len(src().Parts) > 1 && pg.OnPart != nil }
	hasNext := func() bool { s := src(); return hasParts() && s.Part+1 < len(s.Parts) }
	hasQualities := func() bool { return pg.OnQuality != nil && len(src().Qualities) > 1 }
	hasTracks := func() bool { return countTracks(p, player.Subtitle) > 0 || countTracks(p, player.Audio) > 1 }
	when := func(cond func() bool, n core.Node) core.Node {
		return core.Show{When: cond, Then: func(*core.Scope) core.Node { return n }}
	}
	parts := darkButton(cx, "parts", locales.Parts(cx), "h-8 flex-row gap-1.5 px-2.5", toggle("parts"),
		core.Icon{SVG: lucide.ListVideo(), Class: "size-[18px] text-white"},
		core.Text{ContentFunc: func() string {
			s := src()
			return fmt.Sprintf("%s/%d", s.Parts[min(s.Part, len(s.Parts)-1)].Label, len(s.Parts))
		}, Class: white},
	)
	parts.ClassFunc = open("parts", "")
	speed := darkButton(cx, "rate", locales.Speed(cx), "h-8 px-2.5", toggle("speed"),
		core.Text{ContentFunc: func() string { return rateLabel(p.Rate()) }, Class: "text-[13px] font-semibold text-white"})
	speed.ClassFunc = open("speed", "")
	tracks := darkButton(cx, "tracks", locales.SubtitlesAndAudio(cx), "size-9", toggle("tracks"),
		core.Icon{SVG: lucide.Captions(), Class: "size-5 text-white"})
	tracks.ClassFunc = open("tracks", "")
	quality := darkButton(cx, "quality", locales.Quality(cx), "h-8 px-2.5", toggle("quality"),
		core.Text{ContentFunc: func() string { return qualityLabel(src()) }, Class: "text-[13px] font-semibold text-white"})
	quality.ClassFunc = open("quality", "")
	return core.View{
		Class: "absolute right-0 bottom-0 left-0 gap-2 bg-linear-to-t from-black/80 via-black/45 to-transparent px-5 pt-14 pb-4",
		// The gradient lets presses through to what is under it, as the
		// resume offer; its controls take theirs.
		PointerEvents: core.PointerEventsBoxNone,
		Children: []core.Node{
			track("seek", locales.Position(cx), "", share, buffered, seek, true),
			core.View{Class: "flex-row items-center gap-1", Children: []core.Node{
				labelled(darkButton(cx, "play", "", "size-9", func() { togglePlay(p) }, core.Icon{SVGFunc: func() *core.SVG {
					if playing(p) {
						return lucide.Pause()
					}
					return lucide.Play()
				}, Class: "size-5 text-white"}), func() string {
					if playing(p) {
						return locales.Pause(cx)
					}
					return locales.Play(cx)
				}),
				when(hasNext, darkButton(cx, "next", locales.NextPart(cx), "size-9", func() { pg.OnPart(src().Part + 1) },
					core.Icon{SVG: lucide.SkipForward(), Class: "size-[18px] text-white"})),
				core.View{Class: "flex-row items-center gap-2 pr-2", Children: []core.Node{
					labelled(darkButton(cx, "mute", "", "size-9", func() { p.SetMuted(!p.Muted()) }, core.Icon{SVGFunc: func() *core.SVG {
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
					track("volume", locales.Volume(cx), "w-20", func() float64 {
						if p.Muted() {
							return 0
						}
						return p.Volume()
					}, nil, func(v float64) {
						p.SetMuted(false)
						p.SetVolume(v)
					}, false),
				}},
				core.View{Class: "flex-row items-center gap-1", Children: []core.Node{
					core.Text{TestID: "position", ContentFunc: func() string { return clock(p.Position()) }, Class: white + " text-white/90", Style: timeStyle},
					core.Text{Content: "/", Class: white + " text-white/90"},
					core.Text{TestID: "duration", ContentFunc: func() string { return clock(p.Duration()) }, Class: white + " text-white/90", Style: timeStyle},
				}},
				core.View{Class: "flex-1"},
				when(hasParts, parts),
				speed,
				when(hasTracks, tracks),
				when(hasQualities, quality),
				labelled(darkButton(cx, "fullscreen", "", "size-9", func() { desktop.ToggleFullScreen(cx) }, core.Icon{SVGFunc: func() *core.SVG {
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
			}},
		},
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
func menus(cx *core.Scope, p *player.Player, src func() Source, menu *core.Signal[string], pg Page) core.Node {
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
			return panel("right-[140px] w-32", locales.Speed(cx), rows...)
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
			return panel("right-[52px] w-44", locales.Quality(cx), rows...)
		case "tracks":
			return tracksPanel(cx, p, menu)
		case "parts":
			return partsPanel(cx, src, menu, pg)
		}
		return nil
	})
}

// panel is a menu over the picture, above the controls at its right.
func panel(class, title string, rows ...core.Node) core.Node {
	return core.View{
		TestID: "menu",
		Class:  "absolute bottom-[64px] gap-0.5 rounded-xl border border-white/10 bg-zinc-900/90 p-1.5 shadow-2xl " + class,
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
		Class:   "group h-8 flex-row items-center gap-2 rounded-lg px-2.5 hover:bg-white/10 selected:bg-white/10",
		Children: []core.Node{
			core.Text{Content: label, MaxLines: 1, Class: "flex-1 text-[13px] text-white/90 group-selected:font-medium group-selected:text-white"},
			core.Show{When: checked, Then: func(*core.Scope) core.Node {
				return core.Icon{SVG: lucide.Check(), Class: "size-4 text-accent"}
			}},
		},
	}
}

// tracksPanel chooses the subtitle and audio tracks, each by its title
// or else its number.
func tracksPanel(cx *core.Scope, p *player.Player, menu *core.Signal[string]) core.Node {
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
		TestID:   "menu",
		Class:    "absolute right-[100px] bottom-[64px] w-[380px] flex-row gap-1 rounded-xl border border-white/10 bg-zinc-900/90 p-1.5 shadow-2xl",
		Children: columns,
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
func partsPanel(cx *core.Scope, src func() Source, menu *core.Signal[string], pg Page) core.Node {
	s := src()
	var rows []core.Node
	for i, pt := range s.Parts {
		current := i == s.Part
		length := ""
		if pt.Duration > 0 {
			length = clock(pt.Duration)
		}
		label, title, sub := "w-8 text-xs font-semibold text-white/50", "text-[13px] text-white/90", "text-xs text-white/40"
		box := "flex-row items-center gap-3 rounded-xl px-2.5 py-2 hover:bg-white/10"
		if current {
			label, title, sub = "w-8 text-xs font-semibold text-accent", "text-[13px] font-medium text-white", "text-xs text-accent"
			box += " bg-white/10"
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
			children = append(children, core.Icon{SVG: lucide.AudioLines(), Class: "size-4 text-accent"})
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
	return core.View{
		TestID: "menu",
		Class:  "absolute top-4 right-4 bottom-[76px] w-[300px] gap-1 rounded-2xl border border-white/10 bg-zinc-900/90 p-2 shadow-2xl",
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
