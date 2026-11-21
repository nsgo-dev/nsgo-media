package pipeline

import (
	"bytes"
	"errors"
	"fmt"
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

// Cue is a subtitle's text shown from Start to End.
type Cue struct {
	Start, End time.Duration
	Text       string
}

// cueStore keeps each subtitle track's cues in start order, as they are
// demuxed or parsed; the same cue met again after a seek is kept once.
type cueStore struct {
	mu     sync.Mutex
	tracks map[int][]Cue
}

func (s *cueStore) add(track int, c Cue) {
	if c.Text == "" || c.End <= c.Start {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tracks == nil {
		s.tracks = map[int][]Cue{}
	}
	cues := s.tracks[track]
	i, _ := slices.BinarySearchFunc(cues, c.Start, func(a Cue, t time.Duration) int { return compareDur(a.Start, t) })
	for j := i; j < len(cues) && cues[j].Start == c.Start; j++ {
		if cues[j].Text == c.Text {
			return
		}
	}
	s.tracks[track] = slices.Insert(cues, i, c)
}

func (s *cueStore) set(track int, cues []Cue) {
	slices.SortStableFunc(cues, func(a, b Cue) int { return compareDur(a.Start, b.Start) })
	s.mu.Lock()
	if s.tracks == nil {
		s.tracks = map[int][]Cue{}
	}
	s.tracks[track] = cues
	s.mu.Unlock()
}

// at returns the track's cues showing at t.
func (s *cueStore) at(track int, t time.Duration) []Cue {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Cue
	for _, c := range s.tracks[track] {
		if c.Start > t {
			break
		}
		if t < c.End {
			out = append(out, c)
		}
	}
	return out
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
// extension, from UTF-8 or UTF-16 with a byte order mark.
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
