package playback

import (
	"context"
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/oneo-me/nsgo-media/player"

	"github.com/oneo-me/nsgo-media/app/internal/store"
)

// Comment is a danmaku comment: its text shown over the picture from its
// time, scrolling across or fixed at the top or the bottom.
type Comment struct {
	At   time.Duration
	Mode Mode
	// Color is the text's, white when !Colored.
	Color   core.Color
	Colored bool
	Text    string
}

// Mode is where a comment shows.
type Mode uint8

// The comment modes.
const (
	Scroll Mode = iota
	Top
	Bottom
)

// DanmakuLoader loads the comments of the span from from to to of a
// source.
type DanmakuLoader func(ctx context.Context, from, to time.Duration) ([]Comment, error)

const (
	// segment is the span the comments load in, and ahead how far before
	// the next one's start it loads.
	segment = 6 * time.Minute
	ahead   = 30 * time.Second
	// fixedFor is how long a fixed comment shows.
	fixedFor = 4 * time.Second
	// gap keeps scrolling comments of a lane apart, in points.
	gap = 24
	// jitter is how far back the position may step, as at the end, without
	// being a seek.
	jitter = 250 * time.Millisecond
)

// scrollFor is how long a scrolling comment takes across the picture at
// each speed: slow, normal and fast.
var scrollFor = map[int]time.Duration{-1: 11 * time.Second, 0: 8 * time.Second, 1: 5500 * time.Millisecond}

// fontSizes are the comments' sizes: small, medium and large.
var fontSizes = map[int]core.Points{-1: 14, 0: 17, 1: 22}

// shown is a comment laid out: its lane, its width and when it started.
type shown struct {
	n     int
	c     Comment
	lane  int
	width core.Points
}

// danmaku holds the comments of the source playing and lays out those
// due as the player's position moves.
type danmaku struct {
	cx       *core.Scope
	p        *player.Player
	settings func() store.Danmaku
	// compact draws the comments smaller, as in a pinned window.
	compact func() bool
	size    *core.Signal[core.Size]

	load     DanmakuLoader
	ctx      context.Context
	cancel   func()
	loaded   map[time.Duration]bool
	comments []Comment
	// next is the index of the first comment not yet laid out, at the
	// time laid out to, and last the position seen last.
	next int
	at   time.Duration
	last time.Duration
	seq  int
	// active are those showing.
	active *core.Signal[[]shown]
}

func newDanmaku(cx *core.Scope, p *player.Player, settings func() store.Danmaku, compact func() bool) *danmaku {
	d := &danmaku{cx: cx, p: p, settings: settings, compact: compact, size: core.NewSignal(cx, core.Size{}),
		active: core.NewSignalFunc[[]shown](cx, nil, nil), cancel: func() {}}
	cx.Effect(func() {
		pos := p.Position()
		cx.Untrack(func() { d.advance(pos) })
	})
	// Changes to how comments show lay them out again.
	cx.Effect(func() {
		settings()
		compact()
		d.size.Get()
		cx.Untrack(d.reset)
	})
	cx.Cleanup(func() { d.cancel() })
	return d
}

// open starts over with the comments load answers, nil for none.
func (d *danmaku) open(load DanmakuLoader) {
	d.cancel()
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.load, d.loaded, d.comments = load, map[time.Duration]bool{}, nil
	d.seq++
	d.reset()
	d.fetch(0)
}

// fetch loads the segment holding at, once.
func (d *danmaku) fetch(at time.Duration) {
	from := at / segment * segment
	if d.load == nil || d.loaded[from] {
		return
	}
	if dur := core.Untracked(d.cx, d.p.Duration); dur > 0 && from >= dur {
		return
	}
	d.loaded[from] = true
	load, ctx, n := d.load, d.ctx, d.seq
	go func() {
		list, err := load(ctx, from, from+segment)
		d.cx.Post(func() {
			if n != d.seq {
				return
			}
			if err != nil {
				log.Printf("danmaku: %v", err)
				delete(d.loaded, from)
				return
			}
			d.comments = append(d.comments, list...)
			slices.SortStableFunc(d.comments, func(a, b Comment) int { return int(a.At - b.At) })
			d.reset()
		})
	}()
}

// reset lays the comments out again from the position, and those of
// the last few seconds, which would still show.
func (d *danmaku) reset() { d.resetFrom(fixedFor) }

// resetFrom lays the comments out again from back before the position.
func (d *danmaku) resetFrom(back time.Duration) {
	d.last = core.Untracked(d.cx, d.p.Position)
	d.at = max(d.last-back, 0)
	d.next, _ = slices.BinarySearchFunc(d.comments, d.at, func(c Comment, t time.Duration) int { return int(c.At - t) })
	d.active.Set(nil)
}

// visible reports whether the settings show c.
func visible(s store.Danmaku, c Comment) bool {
	switch {
	case s.Off, c.Colored && s.HideColor:
		return false
	case c.Mode == Top:
		return !s.HideTop
	case c.Mode == Bottom:
		return !s.HideBottom
	}
	return !s.HideScroll
}

func (d *danmaku) fontSize() core.Points {
	size := fontSizes[d.settings().Size]
	if d.compact() {
		size = size * 3 / 4
	}
	return size
}

// top is where the comments' first lane starts, under the top bar.
func (d *danmaku) top() core.Points {
	if d.compact() {
		return 40
	}
	return 52
}

func (d *danmaku) lineHeight() core.Points { return d.fontSize() * 1.5 }

func (d *danmaku) duration() time.Duration { return scrollFor[d.settings().Speed] }

// advance lays out the comments due by pos, dropping those that ended. A
// jump back or of more than a second starts over there.
func (d *danmaku) advance(pos time.Duration) {
	d.fetch(pos)
	if pos+ahead >= (pos/segment+1)*segment {
		d.fetch(pos + ahead)
	}
	if pos < d.last-jitter || pos-d.last > time.Second {
		// A seek shows the comments from where it went.
		d.resetFrom(0)
	}
	s := d.settings()
	size := d.size.GetUntracked()
	if size.Width <= 0 || s.Off {
		d.at, d.last = pos, pos
		if len(d.active.GetUntracked()) > 0 {
			d.active.Set(nil)
		}
		return
	}
	dur := d.duration()
	active := d.active.GetUntracked()
	kept := active[:0:0]
	for _, a := range active {
		end := a.c.At + fixedFor
		if a.c.Mode == Scroll {
			end = a.c.At + dur
		}
		if end > pos {
			kept = append(kept, a)
		}
	}
	changed := len(kept) != len(active)
	line := d.lineHeight()
	lanes := max(int((size.Height*core.Points(s.Quarters())/4-d.top())/line), 1)
	fixedLanes := max(int(size.Height/2/line), 1)
	font := core.Font{Size: d.fontSize(), Weight: core.FontWeightSemibold}
	for ; d.next < len(d.comments) && d.comments[d.next].At <= pos; d.next++ {
		c := d.comments[d.next]
		if c.At < d.at || !visible(s, c) {
			continue
		}
		w := core.Points(d.cx.MeasureText(c.Text, font).Width)
		lane := -1
		switch c.Mode {
		case Scroll:
			lane = scrollLane(kept, c, w, size.Width, dur, lanes)
		default:
			lane = fixedLane(kept, c.Mode, fixedLanes)
		}
		if lane < 0 {
			continue
		}
		kept = append(kept, shown{n: d.next, c: c, lane: lane, width: w})
		changed = true
	}
	d.at, d.last = pos, pos
	if changed {
		d.active.Set(kept)
	}
}

// speed is how fast a comment of width w crosses a picture width wide in
// dur, in points a second.
func speed(w, width core.Points, dur time.Duration) core.Points {
	return (width + w) / core.Points(dur.Seconds())
}

// scrollLane is the first lane, from the top, where c neither overlaps
// the last comment nor catches up with it before it leaves; -1 for none.
func scrollLane(active []shown, c Comment, w, width core.Points, dur time.Duration, lanes int) int {
	last := map[int]shown{}
	for _, a := range active {
		if a.c.Mode == Scroll && (last[a.lane].c.At <= a.c.At) {
			last[a.lane] = a
		}
	}
	for lane := range lanes {
		prev, ok := last[lane]
		if !ok {
			return lane
		}
		// The last one's tail has come in, and c reaches the left edge
		// after the last one left.
		in := core.Points((c.At - prev.c.At).Seconds()) * speed(prev.width, width, dur)
		if in < prev.width+gap {
			continue
		}
		reaches := c.At + time.Duration(float64(dur)*float64(width/(width+w)))
		if reaches >= prev.c.At+dur {
			return lane
		}
	}
	return -1
}

// fixedLane is the first lane of mode free, from the top for Top and
// from the bottom for Bottom; -1 for none.
func fixedLane(active []shown, mode Mode, lanes int) int {
	taken := map[int]bool{}
	for _, a := range active {
		if a.c.Mode == mode {
			taken[a.lane] = true
		}
	}
	for lane := range lanes {
		if !taken[lane] {
			return lane
		}
	}
	return -1
}

// layer draws the comments over the picture, below the bars, which lift
// the bottom ones above the controls while they show.
func (d *danmaku) layer(bars func() bool) core.Node {
	return core.View{
		TestID:        "danmaku",
		Class:         "absolute inset-0 overflow-hidden",
		PointerEvents: core.PointerEventsNone,
		OnLayout:      func(b core.Bounds) { d.size.Set(b.Size) },
		StyleFunc:     func() core.Style { return core.Style{Opacity: core.Alpha(float32(d.settings().Opacity()))} },
		Children: []core.Node{core.Each(d.active.Get, func(s shown) int { return s.n }, func(cx *core.Scope, item func() shown, _ func() int) core.Node {
			s := item()
			color := core.RGB(0xffffff)
			if s.c.Colored {
				color = s.c.Color
			}
			return core.Text{
				TestID:   "comment-" + strconv.Itoa(s.n),
				Content:  s.c.Text,
				MaxLines: 1,
				Class:    "absolute font-semibold",
				StyleFunc: func() core.Style {
					size, line := d.fontSize(), d.lineHeight()
					st := core.Style{FontSize: size, Color: color, TextShadowColor: core.RGBA(0x000000, 0.95), TextShadowOffset: core.Point{Y: 1}, TextShadowRadius: 2}
					st.Width = core.Pt(s.width + 4)
					area := d.size.Get()
					switch s.c.Mode {
					case Scroll:
						at := core.Points((d.p.Position() - s.c.At).Seconds())
						st.Top = core.Pt(d.top() + core.Points(s.lane)*line)
						st.Left = core.Pt(area.Width - at*speed(s.width, area.Width, d.duration()))
					case Top:
						st.Top = core.Pt(d.top() + core.Points(s.lane)*line)
						st.Left = core.Pt((area.Width - s.width) / 2)
					case Bottom:
						bottom := core.Points(12)
						if bars() {
							bottom = 84
						}
						st.Bottom = core.Pt(bottom + core.Points(s.lane)*line)
						st.Left = core.Pt((area.Width - s.width) / 2)
					}
					return st
				},
			}
		})},
	}
}
