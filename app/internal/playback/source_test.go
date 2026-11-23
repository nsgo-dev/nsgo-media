package playback

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  Source
		ok   bool
	}{
		{"path", Source{URL: "/movies/a.mkv"}, true},
		{"windows path", Source{URL: `C:\movies\a.mkv`}, true},
		{"https", Source{URL: "https://cdn.example.com/a.mp4"}, true},
		{"separate audio", Source{URL: "https://cdn.example.com/v.m4s", Audio: &Stream{URL: "https://cdn.example.com/a.m4s"}}, true},
		{"empty", Source{URL: " "}, false},
		{"other scheme", Source{URL: "ftp://example.com/a.mp4"}, false},
		{"no host", Source{URL: "https:///a.mp4"}, false},
		{"bad audio", Source{URL: "/a.mkv", Audio: &Stream{URL: ""}}, false},
		{"subtitle text", Source{URL: "/a.mkv", Subtitles: []Subtitle{{Label: "A", Text: "WEBVTT\n"}}}, true},
		{"bad subtitles", Source{URL: "/a.mkv", Subtitles: []Subtitle{{Label: "A"}}}, false},
	} {
		if err := tc.src.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v", tc.name, err)
		}
	}
}

func TestSourceKeyAndTitle(t *testing.T) {
	for _, tc := range []struct {
		src        Source
		key, title string
	}{
		{Source{URL: "/movies/a.mkv"}, "/movies/a.mkv", "a.mkv"},
		{Source{URL: "https://cdn.example.com/v/b.mp4?sig=1"}, "https://cdn.example.com/v/b.mp4?sig=1", "b.mp4"},
		{Source{URL: "https://example.com/"}, "https://example.com/", "example.com"},
		{Source{Key: "site:42", Title: "Part 1", URL: "https://cdn.example.com/x"}, "site:42", "Part 1"},
	} {
		if k := tc.src.key(); k != tc.key {
			t.Errorf("%s: key %q; want %q", tc.src.URL, k, tc.key)
		}
		if title := tc.src.title(); title != tc.title {
			t.Errorf("%s: title %q; want %q", tc.src.URL, title, tc.title)
		}
	}
}

func TestSourceOptions(t *testing.T) {
	if n := len((Source{URL: "/a.mkv"}).options()); n != 0 {
		t.Fatalf("a plain file has %d options", n)
	}
	h := http.Header{"Referer": {"https://example.com/"}}
	src := Source{URL: "https://cdn.example.com/v", Headers: h, Audio: &Stream{URL: "https://cdn.example.com/a", Headers: h}}
	if n := len(src.options()); n != 2 {
		t.Fatalf("%d options; want headers and audio", n)
	}
}

func TestResumable(t *testing.T) {
	for _, tc := range []struct {
		pos, d time.Duration
		ok     bool
	}{
		{5 * time.Second, time.Hour, false},
		{time.Minute, time.Hour, true},
		{time.Hour - 5*time.Second, time.Hour, false},
		{time.Minute, 0, true},
	} {
		if got := resumable(tc.pos, tc.d); got != tc.ok {
			t.Errorf("resumable(%v, %v) = %v", tc.pos, tc.d, got)
		}
	}
}

func TestSubtitleExt(t *testing.T) {
	for _, tc := range []struct {
		path, ctype, data, ext string
	}{
		{"/a.SRT", "", "", ".srt"},
		{"/sub", "text/vtt; charset=utf-8", "", ".vtt"},
		{"/sub", "", "\xef\xbb\xbfWEBVTT\n", ".vtt"},
		{"/sub", "", "[Script Info]\n", ".ass"},
		{"/sub", "text/plain", "1\n00:00:01,000 --> 00:00:02,000\nHi\n", ".srt"},
	} {
		if ext := subtitleExt(tc.path, tc.ctype, []byte(tc.data)); ext != tc.ext {
			t.Errorf("subtitleExt(%q, %q) = %q; want %q", tc.path, tc.ctype, ext, tc.ext)
		}
	}
	for label, want := range map[string]string{"中文 (CC)": "中文 (CC)", "a/b:c": "a_b_c", " .. ": "subtitles"} {
		if got := fileName(label); got != want {
			t.Errorf("fileName(%q) = %q; want %q", label, got, want)
		}
	}
}

func TestSubtitleFileOfText(t *testing.T) {
	dir := t.TempDir()
	text := "WEBVTT\n\n00:00.000 --> 00:01.000\nHi\n"
	file, err := subtitleFile(context.Background(), nil, dir, 2, Subtitle{Label: "中文/AI", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "2", "中文_AI.vtt"); file != want {
		t.Fatalf("file %s, want %s", file, want)
	}
	if raw, _ := os.ReadFile(file); string(raw) != text {
		t.Fatalf("file holds %q", raw)
	}
}
