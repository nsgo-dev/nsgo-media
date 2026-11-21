// Command demo is a player app with its own controls, showing how an app
// drives NSGo Media's core: the core draws the picture, the app everything
// else.
//
//	go tool nsgo dev examples/demo              # pick a file in the app
//	go run ./examples/demo movie.mkv            # or open one at start
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/player"
)

// startSource is the file or URL to open at start: the first argument, or
// NSGO_MEDIA_DEMO_SOURCE.
func startSource() string {
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return os.Getenv("NSGO_MEDIA_DEMO_SOURCE")
}

var rates = []float64{0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4}

// Root is the main window's content.
func Root(cx *core.Scope) core.Node {
	p := player.New(cx)
	fit := core.NewSignal(cx, player.Contain)
	url := core.NewSignal(cx, "")
	if src := startSource(); src != "" {
		p.Open(src)
		p.Play()
	}
	open := func(path string) {
		if isSubtitle(path) {
			if p.AddSubtitles(path) == nil {
				tracks := p.Info().Tracks
				p.SelectSubtitle(tracks[len(tracks)-1].ID)
			}
			return
		}
		p.Open(path)
		p.Play()
	}
	return core.View{
		Style: core.Style{FlexGrow: 1, Background: core.RGB(0x000000)},
		OnFileDrop: func(uris []string) {
			for _, u := range uris {
				open(strings.TrimPrefix(u, "file://"))
			}
		},
		Children: []core.Node{
			topBar(cx, p, url, open),
			player.View{
				Player:  p,
				FitFunc: fit.Get,
				TestID:  "video",
				Style:   core.Style{FlexGrow: 1, FlexShrink: 1},
				Children: []core.Node{
					subtitles(p),
					status(p),
				},
			},
			progress(p),
			controls(cx, p, fit),
		},
	}
}

func isSubtitle(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt", ".vtt", ".ass", ".ssa":
		return true
	}
	return false
}

func topBar(cx *core.Scope, p *player.Player, url *core.Signal[string], open func(string)) core.Node {
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.All(8)},
		Children: []core.Node{
			ui.Button{Text: "Open…", Icon: lucide.FolderOpen, TestID: "open", OnPress: func() {
				cx.PickFiles(core.PickFilesOptions{}, func(files []core.File, err error) {
					if err == nil && len(files) > 0 {
						open(files[0].Path)
					}
				})
			}},
			ui.Input{
				Placeholder: "https://…",
				Value:       url.Get,
				OnChange:    url.Set,
				OnSubmit: func(s string) {
					if s = strings.TrimSpace(s); s != "" {
						open(s)
					}
				},
				TestID: "url",
				Style:  core.Style{FlexGrow: 1},
			},
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

// status says why nothing plays: buffering or a failure.
func status(p *player.Player) core.Node {
	return core.View{
		Style: core.Style{
			Position: core.Absolute, Left: core.Pt(0), Right: core.Pt(0), Top: core.Pt(0), Bottom: core.Pt(0),
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
		},
		PointerEvents: core.PointerEventsNone,
		Children: []core.Node{
			core.Show{
				When: func() bool { return p.State() == player.Buffering || p.State() == player.Opening },
				Then: func(*core.Scope) core.Node { return ui.Spinner{Size: 32} },
			},
			core.Show{
				When: func() bool { return p.State() == player.Failed },
				Then: func(*core.Scope) core.Node {
					return core.Text{TestID: "error", ContentFunc: func() string {
						if err := p.Err(); err != nil {
							return err.Error()
						}
						return ""
					}, Style: core.Style{Color: core.RGB(0xff6b6b), FontSize: 15}}
				},
			},
		},
	}
}

func seconds(d time.Duration) float64 { return d.Seconds() }

// progress is the seek bar, with the buffered range behind it.
func progress(p *player.Player) core.Node {
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Symmetric(0, 12)},
		Children: []core.Node{
			core.Text{TestID: "time", ContentFunc: func() string {
				return clock(p.Position()) + " / " + clock(p.Duration())
			}, Style: core.Style{Color: core.RGB(0xdddddd), FontSize: 13, FontVariant: core.FontTabularNums}},
			core.View{
				Style: core.Style{FlexGrow: 1, JustifyContent: core.JustifyCenter},
				Children: []core.Node{
					core.View{StyleFunc: func() core.Style {
						w := float32(0)
						if d := p.Duration(); d > 0 {
							w = float32(min(p.Buffered().Seconds()/d.Seconds(), 1) * 100)
						}
						return core.Style{
							Position: core.Absolute, Left: core.Pt(0), Height: core.Pt(4), Width: core.Pct(w),
							Background: core.RGBA(0xffffff, 0.25), CornerRadius: 2,
						}
					}},
					ui.Slider{
						TestID:   "seek",
						Label:    "Position",
						Value:    func() float64 { return seconds(p.Position()) },
						MaxFunc:  func() float64 { return max(seconds(p.Duration()), 0.001) },
						OnChange: func(v float64) { p.Seek(time.Duration(v*float64(time.Second)), player.Exact) },
						Style:    core.Style{Width: core.Pct(100)},
					},
				},
			},
		},
	}
}

func controls(cx *core.Scope, p *player.Player, fit *core.Signal[player.Fit]) core.Node {
	playing := func() bool { s := p.State(); return s == player.Playing || s == player.Buffering }
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Padding: core.All(8), Wrap: core.Wrap},
		Children: []core.Node{
			ui.IconButton{
				TestID: "play",
				Icon: func() *core.SVG {
					if playing() {
						return lucide.Pause()
					}
					return lucide.Play()
				},
				LabelFunc: func() string {
					if playing() {
						return "Pause"
					}
					return "Play"
				},
				OnPress: func() {
					if playing() {
						p.Pause()
					} else {
						p.Play()
					}
				},
			},
			ui.IconButton{TestID: "stop", Icon: lucide.Square, Label: "Stop", OnPress: p.Stop},
			ui.IconButton{TestID: "step", Icon: lucide.StepForward, Label: "Next frame", OnPress: func() {
				p.Pause()
				p.StepForward()
			}},
			ui.IconButton{TestID: "loop", Icon: lucide.Repeat, Label: "Loop", Selected: p.Loop, OnPress: func() { p.SetLoop(!p.Loop()) }},
			ui.IconButton{
				TestID: "mute",
				Icon: func() *core.SVG {
					if p.Muted() {
						return lucide.VolumeX()
					}
					return lucide.Volume2()
				},
				Label:   "Mute",
				OnPress: func() { p.SetMuted(!p.Muted()) },
			},
			core.View{Style: core.Style{Width: core.Pt(100)}, Children: []core.Node{
				ui.Slider{TestID: "volume", Label: "Volume", Max: 1, Value: p.Volume, OnChange: p.SetVolume},
			}},
			ui.Select[float64]{
				TestID:   "rate",
				Label:    "Speed",
				Options:  rateOptions(),
				Value:    p.Rate,
				OnChange: p.SetRate,
				Style:    core.Style{Width: core.Pt(84)},
			},
			ui.Select[int]{
				TestID:      "audio",
				Label:       "Audio",
				Placeholder: "Audio",
				OptionsFunc: func() []ui.Option[int] { return trackOptions(p, player.Audio, "No sound") },
				Value:       p.AudioTrack,
				OnChange:    func(id int) { p.SelectAudio(id) },
				Style:       core.Style{Width: core.Pt(150)},
			},
			ui.Select[int]{
				TestID:      "subtitle",
				Label:       "Subtitles",
				Placeholder: "Subtitles",
				OptionsFunc: func() []ui.Option[int] { return trackOptions(p, player.Subtitle, "No subtitles") },
				Value:       p.SubtitleTrack,
				OnChange:    func(id int) { p.SelectSubtitle(id) },
				Style:       core.Style{Width: core.Pt(150)},
			},
			ui.Select[int]{
				TestID:      "chapter",
				Label:       "Chapter",
				Placeholder: "Chapter",
				OptionsFunc: func() []ui.Option[int] { return chapterOptions(p) },
				Value:       func() int { return currentChapter(p) },
				OnChange: func(i int) {
					if info := p.Info(); info != nil && i >= 0 && i < len(info.Chapters) {
						p.Seek(info.Chapters[i].Start, player.Fast)
					}
				},
				Style: core.Style{Width: core.Pt(150)},
			},
			ui.Select[player.Fit]{
				TestID: "fit",
				Label:  "Fit",
				Options: []ui.Option[player.Fit]{
					{Value: player.Contain, Label: "Contain"},
					{Value: player.Cover, Label: "Cover"},
					{Value: player.Fill, Label: "Fill"},
				},
				Value:    fit.Get,
				OnChange: fit.Set,
				Style:    core.Style{Width: core.Pt(104)},
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

func trackOptions(p *player.Player, kind player.Kind, none string) []ui.Option[int] {
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
			label = fmt.Sprintf("Track %d", n)
		}
		if t.Language != "" && t.Language != "und" {
			label += " (" + t.Language + ")"
		}
		opts = append(opts, ui.Option[int]{Value: t.ID, Label: label})
	}
	return opts
}

func chapterOptions(p *player.Player) []ui.Option[int] {
	info := p.Info()
	if info == nil {
		return nil
	}
	var opts []ui.Option[int]
	for i, c := range info.Chapters {
		label := c.Title
		if label == "" {
			label = fmt.Sprintf("Chapter %d", i+1)
		}
		opts = append(opts, ui.Option[int]{Value: i, Label: clock(c.Start) + "  " + label})
	}
	return opts
}

func currentChapter(p *player.Player) int {
	info := p.Info()
	if info == nil {
		return -1
	}
	pos := p.Position()
	cur := -1
	for i, c := range info.Chapters {
		if c.Start <= pos {
			cur = i
		}
	}
	return cur
}

// clock writes d as m:ss or h:mm:ss.
func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func main() { nsgo.Run(Root, nsgo.WindowSize(1024, 640)) }
