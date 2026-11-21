package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Cue is a subtitle shown from Start to End: a text, or the pictures of
// a bitmap track on a canvas of Canvas's size.
type Cue struct {
	// End is openEnd while a bitmap cue's end is not read: it shows until
	// the next cue of its track.
	Start, End time.Duration
	// Gen is the generation it was decoded in, 0 for a file's.
	Gen     uint64
	Text    string
	Canvas  image.Point
	Bitmaps []Bitmap
	// sure is an end the cue gave or the next cue read after it set;
	// otherwise End is only the latest it may end, as DVB's time-out.
	sure bool
}

// Bitmap is a picture of a cue at X, Y on its canvas.
type Bitmap struct {
	X, Y  int
	Image *image.RGBA
}

const openEnd = time.Duration(math.MaxInt64)

// Bounds of the bitmap cues kept: those of the chosen track within
// bitmapWindow of the position, at most bitmapBytes of pictures.
const (
	bitmapWindow = 30 * time.Second
	bitmapBytes  = 64 << 20
)

// cueStore keeps each subtitle track's cues in start order, as they are
// demuxed or parsed; the same cue met again after a seek is kept once.
//
// A bitmap track's cue replaces the one before it, and may come with no
// end, as PGS' do. Such a cue shows only within the span the main input
// has read without a break since it, where no other cue came; a seek
// beyond the span leaves it unshown until it is read again.
type cueStore struct {
	mu      sync.Mutex
	tracks  map[int][]Cue
	bitmap  map[int]bool
	files   map[int]*bitmapFile
	bytes   map[int]int // bitmap tracks' pictures
	spanGen uint64      // the generation the span was last read in
	spanOK  bool
	from    time.Duration
	to      time.Duration // openEnd once the input ended
}

func (s *cueStore) init() {
	if s.tracks == nil {
		s.tracks, s.bitmap, s.files, s.bytes = map[int][]Cue{}, map[int]bool{}, map[int]*bitmapFile{}, map[int]int{}
	}
}

func (s *cueStore) setBitmap(track int) {
	s.mu.Lock()
	s.init()
	s.bitmap[track] = true
	s.mu.Unlock()
}

// read records the main input reading a packet at t in generation gen.
// A read starting within the span goes on with it.
func (s *cueStore) read(gen uint64, t time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.spanGen || !s.spanOK {
		s.spanGen = gen
		if !s.spanOK || t < s.from || t > s.to {
			s.spanOK, s.from, s.to = true, t, t
		}
	}
	s.to = max(s.to, t)
}

// ended records the main input reaching its end in generation gen.
func (s *cueStore) ended(gen uint64) {
	s.mu.Lock()
	if gen == s.spanGen && s.spanOK {
		s.to = openEnd
	}
	s.mu.Unlock()
}

func (s *cueStore) inSpan(start, t time.Duration) bool {
	return s.spanOK && s.from <= start && t <= s.to
}

func (s *cueStore) add(track int, c Cue) {
	if c.Text == "" || c.End <= c.Start {
		return
	}
	c.sure = true
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	cues := s.tracks[track]
	i, _ := slices.BinarySearchFunc(cues, c.Start, func(a Cue, t time.Duration) int { return compareDur(a.Start, t) })
	for j := i; j < len(cues) && cues[j].Start == c.Start; j++ {
		if cues[j].Text == c.Text {
			return
		}
	}
	s.tracks[track] = slices.Insert(cues, i, c)
}

// addBitmap adds a bitmap track's cue, just read, which ends the cues
// before it; one with no bitmaps only ends them.
func (s *cueStore) addBitmap(track int, c Cue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	cues := s.tracks[track]
	for j := range cues {
		p := &cues[j]
		if p.Start < c.Start && c.Start < p.End && s.inSpan(p.Start, c.Start) {
			p.End, p.sure = c.Start, true
		}
	}
	if len(c.Bitmaps) == 0 || c.End <= c.Start {
		return
	}
	i, found := slices.BinarySearchFunc(cues, c.Start, func(a Cue, t time.Duration) int { return compareDur(a.Start, t) })
	if found {
		return // read again after a seek
	}
	if i < len(cues) && cues[i].Start < c.End {
		c.End, c.sure = cues[i].Start, true
	}
	s.tracks[track] = slices.Insert(cues, i, c)
	s.bytes[track] += c.size()
}

func (c *Cue) size() int {
	n := 0
	for _, b := range c.Bitmaps {
		n += len(b.Image.Pix)
	}
	return n
}

// choose forgets the bitmap cues of the track chosen before, and the
// span read while the newly chosen one was not decoding.
func (s *cueStore) choose(old int) {
	s.mu.Lock()
	if s.bitmap[old] {
		delete(s.tracks, old)
		delete(s.bytes, old)
	}
	s.spanOK = false
	s.mu.Unlock()
}

func (s *cueStore) set(track int, cues []Cue) {
	slices.SortStableFunc(cues, func(a, b Cue) int { return compareDur(a.Start, b.Start) })
	for i := range cues {
		cues[i].sure = true
	}
	s.mu.Lock()
	s.init()
	s.tracks[track] = cues
	s.mu.Unlock()
}

func (s *cueStore) setFile(track int, f *bitmapFile) {
	s.mu.Lock()
	s.init()
	s.files[track] = f
	s.mu.Unlock()
}

// at returns the track's cues showing at t, a bitmap file's turned by
// rot. It forgets a bitmap track's cues far from t.
func (s *cueStore) at(track int, t time.Duration, rot int) []Cue {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.files[track]; f != nil {
		if c, ok := f.at(t, rot); ok {
			return []Cue{c}
		}
		return nil
	}
	if s.bitmap[track] {
		s.prune(track, t)
	}
	var out []Cue
	for _, c := range s.tracks[track] {
		if c.Start > t {
			break
		}
		if t < c.End && (c.sure || s.inSpan(c.Start, t)) {
			out = append(out, c)
		}
	}
	return out
}

// prune drops a bitmap track's cues that ended or start more than
// bitmapWindow from t, then the farthest while they hold too many bytes.
func (s *cueStore) prune(track int, t time.Duration) {
	cues := s.tracks[track]
	far := func(c Cue) time.Duration {
		switch {
		case c.Start > t:
			return c.Start - t
		case c.End <= t:
			return t - c.End
		}
		return 0
	}
	n := s.bytes[track]
	cues = slices.DeleteFunc(cues, func(c Cue) bool {
		if far(c) > bitmapWindow {
			n -= c.size()
			return true
		}
		return false
	})
	for n > bitmapBytes && len(cues) > 1 {
		i := 0
		if far(cues[len(cues)-1]) > far(cues[0]) {
			i = len(cues) - 1
		}
		n -= cues[i].size()
		cues = slices.Delete(cues, i, i+1)
	}
	s.tracks[track], s.bytes[track] = cues, n
}

func (s *cueStore) close() {
	s.mu.Lock()
	for _, f := range s.files {
		f.close()
	}
	s.files = nil
	s.mu.Unlock()
}

func compareDur(a, b time.Duration) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// assText is the text of an ASS event line as FFmpeg's decoders give it,
// "ReadOrder,Layer,Style,Name,MarginL,MarginR,MarginV,Effect,Text", or a
// Dialogue line of a file, with override tags removed.
func assText(line string, fields int) string {
	parts := strings.SplitN(line, ",", fields)
	if len(parts) < fields {
		return ""
	}
	return plainASS(parts[fields-1])
}

var assTags = regexp.MustCompile(`\{[^}]*\}`)

func plainASS(s string) string {
	s = assTags.ReplaceAllString(s, "")
	s = strings.NewReplacer(`\N`, "\n", `\n`, "\n", `\h`, " ").Replace(s)
	return strings.TrimSpace(s)
}

var htmlTags = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

// plainMarkup drops SRT's and WebVTT's markup, such as <i> and <c.red>.
func plainMarkup(s string) string {
	s = htmlTags.ReplaceAllString(s, "")
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&nbsp;", " ", "&lrm;", "", "&rlm;", "").Replace(s)
	return strings.TrimSpace(s)
}

// ParseSubtitleFile reads an SRT, WebVTT or ASS/SSA file into cues, by its
// extension, from UTF-8 or UTF-16 with a byte order mark. VobSub files
// are bitmaps, which AddSubtitles reads instead.
func ParseSubtitleFile(path string) ([]Cue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := decodeText(data)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt":
		return parseSRT(text)
	case ".vtt":
		return parseVTT(text)
	case ".ass", ".ssa":
		return parseASS(text)
	}
	return nil, fmt.Errorf("subtitles: %s is not an SRT, WebVTT or ASS file", filepath.Base(path))
}

func decodeText(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		big := data[0] == 0xFE
		data = data[2:]
		u := make([]uint16, len(data)/2)
		for i := range u {
			if big {
				u[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
			} else {
				u[i] = uint16(data[2*i+1])<<8 | uint16(data[2*i])
			}
		}
		return normalizeNewlines(string(utf16.Decode(u)))
	}
	s := string(data)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return normalizeNewlines(s)
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

var timingLine = regexp.MustCompile(`^\s*((?:\d+:)?\d{1,2}:\d{2}[.,]\d{1,3})\s*-->\s*((?:\d+:)?\d{1,2}:\d{2}[.,]\d{1,3})`)

// parseClock reads [h:]mm:ss.fff or ,fff.
func parseClock(s string) (time.Duration, error) {
	s = strings.ReplaceAll(s, ",", ".")
	parts := strings.Split(s, ":")
	var h, m int
	var sec float64
	var err error
	switch len(parts) {
	case 3:
		if h, err = strconv.Atoi(parts[0]); err != nil {
			return 0, err
		}
		parts = parts[1:]
		fallthrough
	case 2:
		if m, err = strconv.Atoi(parts[0]); err != nil {
			return 0, err
		}
		if sec, err = strconv.ParseFloat(parts[1], 64); err != nil {
			return 0, err
		}
	default:
		return 0, errors.New("subtitles: bad time " + s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)+0.5), nil
}

// parseBlocks reads SRT's and WebVTT's blank-line separated blocks, each
// with a timing line followed by its text.
func parseBlocks(text string) ([]Cue, error) {
	var cues []Cue
	for _, block := range strings.Split(text, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		for i, l := range lines {
			m := timingLine.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			start, err1 := parseClock(m[1])
			end, err2 := parseClock(m[2])
			if err1 != nil || err2 != nil {
				break
			}
			cues = append(cues, Cue{Start: start, End: end, Text: plainMarkup(strings.Join(lines[i+1:], "\n"))})
			break
		}
	}
	slices.SortStableFunc(cues, func(a, b Cue) int { return compareDur(a.Start, b.Start) })
	return cues, nil
}

func parseSRT(text string) ([]Cue, error) { return parseBlocks(text) }

func parseVTT(text string) ([]Cue, error) {
	if !strings.HasPrefix(strings.TrimPrefix(text, "\uFEFF"), "WEBVTT") {
		return nil, errors.New("subtitles: a WebVTT file starts with WEBVTT")
	}
	return parseBlocks(text)
}

// parseASS reads an ASS or SSA file's [Events] section, finding the text
// by its Format line.
func parseASS(text string) ([]Cue, error) {
	var cues []Cue
	inEvents := false
	fields := []string{"layer", "start", "end", "style", "name", "marginl", "marginr", "marginv", "effect", "text"}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			inEvents = strings.EqualFold(l, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "format":
			fields = fields[:0]
			for _, f := range strings.Split(val, ",") {
				fields = append(fields, strings.ToLower(strings.TrimSpace(f)))
			}
		case "dialogue":
			parts := strings.SplitN(strings.TrimSpace(val), ",", len(fields))
			if len(parts) < len(fields) {
				continue
			}
			var c Cue
			var err error
			for i, f := range fields {
				switch f {
				case "start":
					c.Start, err = parseClock(strings.TrimSpace(parts[i]))
				case "end":
					c.End, err = parseClock(strings.TrimSpace(parts[i]))
				case "text":
					c.Text = plainASS(parts[i])
				}
				if err != nil {
					break
				}
			}
			if err == nil && c.Text != "" {
				cues = append(cues, c)
			}
		}
	}
	if cues == nil && !strings.Contains(text, "[Events]") {
		return nil, errors.New("subtitles: no [Events] section")
	}
	slices.SortStableFunc(cues, func(a, b Cue) int { return compareDur(a.Start, b.Start) })
	return cues, nil
}

func subtitleCodec(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt":
		return "subrip"
	case ".vtt":
		return "webvtt"
	case ".ssa":
		return "ssa"
	}
	return "ass"
}

func baseName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
