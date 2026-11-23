package playback

import (
	"context"
	"fmt"
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

var rates = []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 3}

const (
	// saveEvery is how far playback moves between saved positions.
	saveEvery = 5 * time.Second
	// seekStep and volumeStep are what the arrow keys move.
	seekStep   = 5 * time.Second
	volumeStep = 0.1
	// offerFor is how long the resume offer shows.
	offerFor = 10 * time.Second
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

	bindKeys(cx, p)
	fullScreen := func() bool { return desktop.FullScreen(cx) }
	return core.View{
		TestID: "player",
		Style: core.Style{
			Position: core.Absolute, Left: core.Pt(0), Right: core.Pt(0), Top: core.Pt(0), Bottom: core.Pt(0),
		},
		StyleFunc: func() core.Style { return core.Style{Background: ui.ColorsOf(cx).Background()} },
		Children: []core.Node{
			core.Show{
				When: func() bool { return !fullScreen() },
				Then: func(cx *core.Scope) core.Node { return header(cx, title.Get, pg.OnClose) },
			},
			player.View{
				Player: p,
				TestID: "video",
				Style:  core.Style{FlexGrow: 1, FlexShrink: 1, Background: core.RGB(0x000000)},
				Children: []core.Node{
					subtitles(p),
					status(cx, p, invalid.Get),
					resumeOffer(cx, p, offer),
				},
			},
			progress(cx, p),
			controls(cx, p, qualityMenu(cx, p, func() Source { return *current.Get() }, pg.OnQuality)),
		},
	}
}

// bindKeys gives the page its keyboard shortcuts.
func bindKeys(cx *core.Scope, p *player.Player) {
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
		if desktop.FullScreen(cx) {
			desktop.ToggleFullScreen(cx)
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

// header is the page's title bar: back and the source's title, moving the
// window as the title bar it replaces does.
func header(cx *core.Scope, title func() string, onClose func()) core.Node {
	return core.View{
		Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOn},
		StyleFunc: func() core.Style {
			bar := desktop.TitleBar(cx)
			return core.Style{
				Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
				Height:  core.Pt(ui.HeaderHeight),
				Padding: core.Edges{Left: bar.Left + 8, Right: bar.Right + 8},
			}
		},
		Children: []core.Node{
			ui.IconButton{TestID: "back", Icon: lucide.ArrowLeft, Label: locales.Back(cx), Variant: ui.IconButtonGhost, OnPress: onClose},
			core.Text{TestID: "title", ContentFunc: title, MaxLines: 1, Style: core.Style{FlexShrink: 1, FontWeight: core.FontWeightSemibold}},
		},
	}
}

// subtitles draws the chosen track's cues over the picture.
func subtitles(p *player.Player) core.Node {
	return core.View{
		Style: core.Style{
			Position: core.Absolute, Left: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(24),
			AlignItems: core.AlignCenter,
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
			Style: core.Style{
				Color: core.RGB(0xffffff), FontSize: 22, TextAlign: core.TextAlignCenter,
				TextShadowColor: core.RGBA(0x000000, 0.9), TextShadowRadius: 3,
			},
		}},
	}
}

// status says why nothing plays: opening, buffering or a failure.
func status(cx *core.Scope, p *player.Player, invalid func() bool) core.Node {
	failed := func() bool { return invalid() || p.State() == player.Failed }
	return core.View{
		Style: core.Style{
			Position: core.Absolute, Left: core.Pt(0), Right: core.Pt(0), Top: core.Pt(0), Bottom: core.Pt(0),
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 8, Padding: core.All(24),
		},
		PointerEvents: core.PointerEventsNone,
		Children: []core.Node{
			core.Show{
				When: func() bool { return !invalid() && (p.State() == player.Buffering || p.State() == player.Opening) },
				Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 32} },
			},
			core.Show{
				When: failed,
				Then: func(cx *core.Scope) core.Node {
					return core.Text{TestID: "error", ContentFunc: func() string {
						if err := p.Err(); err != nil && !invalid() {
							return locales.CannotPlay(cx) + "\n" + err.Error()
						}
						return locales.CannotPlay(cx)
					}, Style: core.Style{Color: core.RGB(0xffffff), FontSize: 15, TextAlign: core.TextAlignCenter}}
				},
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
				return core.View{
					Style: core.Style{
						Position: core.Absolute, Left: core.Pt(16), Bottom: core.Pt(16),
						Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4,
					},
					Children: []core.Node{
						ui.Button{
							TestID:  "resume",
							Variant: ui.ButtonSecondary,
							Text:    locales.ResumeAt(cx, clock(at)),
							OnPress: func() {
								p.Seek(at, player.Exact)
								offer.Set(0)
							},
						},
						ui.IconButton{TestID: "dismiss-resume", Icon: lucide.X, Label: locales.Dismiss(cx), OnPress: func() { offer.Set(0) }},
					},
				}
			},
		}
	})
}

// progress is the seek bar, with the buffered range behind it, between
// the position and the duration.
func progress(cx *core.Scope, p *player.Player) core.Node {
	timeStyle := core.Style{FontSize: 13, FontVariant: core.FontTabularNums}
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Edges{Top: 8, Left: 12, Right: 12}},
		Children: []core.Node{
			core.Text{TestID: "position", ContentFunc: func() string { return clock(p.Position()) }, Style: timeStyle},
			core.View{
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, JustifyContent: core.JustifyCenter},
				Children: []core.Node{
					core.View{StyleFunc: func() core.Style {
						w := float32(0)
						if d := p.Duration(); d > 0 {
							w = float32(min(p.Buffered().Seconds()/d.Seconds(), 1) * 100)
						}
						return core.Style{
							Position: core.Absolute, Left: core.Pt(0), Height: core.Pt(4), Width: core.Pct(w),
							Background: ui.ColorsOf(cx).Muted().Faded(0.35), CornerRadius: 2,
						}
					}},
					ui.Slider{
						TestID:   "seek",
						Label:    locales.Position(cx),
						Value:    func() float64 { return p.Position().Seconds() },
						MaxFunc:  func() float64 { return max(p.Duration().Seconds(), 0.001) },
						OnChange: func(v float64) { p.Seek(time.Duration(v*float64(time.Second)), player.Exact) },
						Style:    core.Style{Width: core.Pct(100)},
					},
				},
			},
			core.Text{TestID: "duration", ContentFunc: func() string { return clock(p.Duration()) }, Style: timeStyle},
		},
	}
}

// qualityMenu chooses among the source's qualities, when it has some.
func qualityMenu(cx *core.Scope, p *player.Player, src func() Source, onQuality func(string, time.Duration)) core.Node {
	return core.Show{
		When: func() bool { return onQuality != nil && len(src().Qualities) > 0 },
		Then: func(cx *core.Scope) core.Node {
			return ui.Select[string]{
				TestID: "quality",
				Label:  locales.Quality(cx),
				OptionsFunc: func() []ui.Option[string] {
					var opts []ui.Option[string]
					for _, q := range src().Qualities {
						opts = append(opts, ui.Option[string]{Value: q.ID, Label: q.Label})
					}
					return opts
				},
				Value: func() string { return src().Quality },
				OnChange: func(id string) {
					if id != src().Quality {
						onQuality(id, p.Position())
					}
				},
				Style: core.Style{Width: core.Pt(110)},
			}
		},
	}
}

func controls(cx *core.Scope, p *player.Player, quality core.Node) core.Node {
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Padding: core.All(8)},
		Children: []core.Node{
			ui.IconButton{
				TestID: "play",
				Icon: func() *core.SVG {
					if playing(p) {
						return lucide.Pause()
					}
					return lucide.Play()
				},
				LabelFunc: func() string {
					if playing(p) {
						return locales.Pause(cx)
					}
					return locales.Play(cx)
				},
				OnPress: func() { togglePlay(p) },
			},
			ui.IconButton{
				TestID: "mute",
				Icon: func() *core.SVG {
					if p.Muted() {
						return lucide.VolumeX()
					}
					return lucide.Volume2()
				},
				LabelFunc: func() string {
					if p.Muted() {
						return locales.Unmute(cx)
					}
					return locales.Mute(cx)
				},
				OnPress: func() { p.SetMuted(!p.Muted()) },
			},
			core.View{Style: core.Style{Width: core.Pt(100), MinWidth: core.Pt(100), FlexShrink: 0}, Children: []core.Node{
				ui.Slider{TestID: "volume", Label: locales.Volume(cx), Max: 1, Value: p.Volume, OnChange: p.SetVolume},
			}},
			core.View{Style: core.Style{FlexGrow: 1}},
			quality,
			ui.Select[float64]{
				TestID:   "rate",
				Label:    locales.Speed(cx),
				Options:  rateOptions(),
				Value:    p.Rate,
				OnChange: p.SetRate,
				Style:    core.Style{Width: core.Pt(80)},
			},
			ui.Select[int]{
				TestID:      "audio",
				Label:       locales.Audio(cx),
				Placeholder: locales.Audio(cx),
				OptionsFunc: func() []ui.Option[int] { return trackOptions(cx, p, player.Audio, locales.NoSound(cx)) },
				Value:       p.AudioTrack,
				OnChange:    func(id int) { p.SelectAudio(id) },
				Style:       core.Style{Width: core.Pt(150)},
			},
			ui.Select[int]{
				TestID:      "subtitle",
				Label:       locales.Subtitles(cx),
				Placeholder: locales.Subtitles(cx),
				OptionsFunc: func() []ui.Option[int] { return trackOptions(cx, p, player.Subtitle, locales.NoSubtitles(cx)) },
				Value:       p.SubtitleTrack,
				OnChange:    func(id int) { p.SelectSubtitle(id) },
				Style:       core.Style{Width: core.Pt(150)},
			},
			ui.IconButton{
				TestID: "fullscreen",
				Icon: func() *core.SVG {
					if desktop.FullScreen(cx) {
						return lucide.Minimize()
					}
					return lucide.Maximize()
				},
				LabelFunc: func() string {
					if desktop.FullScreen(cx) {
						return locales.ExitFullScreen(cx)
					}
					return locales.FullScreen(cx)
				},
				OnPress: func() { desktop.ToggleFullScreen(cx) },
			},
		},
	}
}

func rateOptions() []ui.Option[float64] {
	var opts []ui.Option[float64]
	for _, r := range rates {
		opts = append(opts, ui.Option[float64]{Value: r, Label: fmt.Sprintf("%g×", r)})
	}
	return opts
}

// trackOptions are the tracks of kind to choose from, after none.
func trackOptions(cx *core.Scope, p *player.Player, kind player.Kind, none string) []ui.Option[int] {
	opts := []ui.Option[int]{{Value: -1, Label: none}}
	info := p.Info()
	if info == nil {
		return opts
	}
	n := 0
	for _, t := range info.Tracks {
		if t.Kind != kind {
			continue
		}
		n++
		label := t.Title
		if label == "" {
			label = locales.Track(cx, n)
		}
		if t.Language != "" && t.Language != "und" {
			label += " (" + t.Language + ")"
		}
		opts = append(opts, ui.Option[int]{Value: t.ID, Label: label})
	}
	return opts
}

// clock writes d as m:ss or h:mm:ss.
func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
