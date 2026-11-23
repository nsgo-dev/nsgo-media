package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
)

// folder makes a video folder with a subfolder, videos, subtitles beside
// one and files that are neither.
func folder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{
		"b.mkv", "A.mp4", "A.srt", "A.zh-CN.ass", "Ab.srt", "notes.txt", ".hidden.mp4",
		"Season 1/e01.mkv", "Season 1/e02.mkv", ".cache/x.mp4",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func load(t *testing.T, folders string) *plugin.Plugin {
	t.Helper()
	p, err := Load(plugin.Options{AppVersion: "0.1.0", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := p.SetSetting(context.Background(), "folders", folders); err != nil {
		t.Fatal(err)
	}
	return p
}

func ids(items []plugin.Item) []string {
	var s []string
	for _, it := range items {
		s = append(s, string(it.Kind)+":"+filepath.Base(it.ID))
	}
	return s
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %q, want %q", what, got, want)
		}
	}
}

func TestBrowseFolders(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	p := load(t, dir)
	home, err := p.Home(ctx, plugin.PageParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(home.Sections) != 1 || home.Sections[0].Title != filepath.Base(dir) {
		t.Fatalf("home %+v", home)
	}
	equal(t, "home", ids(home.Sections[0].Items), []string{"collection:Season 1", "video:A.mp4", "video:b.mkv"})

	season, err := p.Browse(ctx, plugin.BrowseParams{ID: filepath.Join(dir, "Season 1")})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "season", ids(season.Sections[0].Items), []string{"video:e01.mkv", "video:e02.mkv"})
}

func TestSearchAndResolve(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	p := load(t, dir)
	res, err := p.Search(ctx, plugin.SearchParams{Query: "E0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sections) != 1 {
		t.Fatalf("search %+v", res)
	}
	equal(t, "search", ids(res.Sections[0].Items), []string{"video:e01.mkv", "video:e02.mkv"})
	res, err = p.Search(ctx, plugin.SearchParams{Query: "season"})
	if err != nil || len(res.Sections) != 1 || len(res.Sections[0].Items) != 1 || res.Sections[0].Items[0].Kind != plugin.KindCollection {
		t.Fatalf("search for a folder = %+v, %v", res, err)
	}

	video := filepath.Join(dir, "b.mkv")
	for _, text := range []string{video, plugin.FileURL(video), `"` + video + `"`} {
		it, err := p.Resolve(ctx, plugin.ResolveParams{Text: text})
		if err != nil || it == nil || it.Kind != plugin.KindVideo || it.ID != video {
			t.Fatalf("resolve %q = %+v, %v", text, it, err)
		}
	}
	for _, text := range []string{"b.mkv", filepath.Join(dir, "notes.txt"), "https://example.com/b.mkv"} {
		if it, err := p.Resolve(ctx, plugin.ResolveParams{Text: text}); err != nil || it != nil {
			t.Fatalf("resolve %q = %+v, %v", text, it, err)
		}
	}
}

func TestVideoAndStream(t *testing.T) {
	ctx := context.Background()
	dir := folder(t)
	p := load(t, dir)
	path := filepath.Join(dir, "A.mp4")
	v, err := p.Video(ctx, plugin.VideoParams{ID: path})
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "A" || len(v.Parts) != 1 {
		t.Fatalf("video %+v", v)
	}
	equal(t, "related", ids(v.Related), []string{"video:b.mkv"})

	s, err := p.Stream(ctx, plugin.StreamParams{Video: path, Part: v.Parts[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := plugin.FilePath(s.Video.URL); !ok || got != path {
		t.Fatalf("stream %q, want %q", s.Video.URL, path)
	}
	var labels []string
	for _, sub := range s.Subtitles {
		labels = append(labels, sub.Label)
	}
	equal(t, "subtitles", labels, []string{"SRT", "zh-CN"})

	if _, err := p.Stream(ctx, plugin.StreamParams{Video: filepath.Join(dir, "gone.mp4"), Part: "file"}); !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("a missing file = %v", err)
	}
}

func TestMissingFoldersAskForSetup(t *testing.T) {
	p := load(t, filepath.Join(t.TempDir(), "none"))
	if _, err := p.Home(context.Background(), plugin.PageParams{}); !errors.Is(err, plugin.ErrSetup) {
		t.Fatalf("home without folders = %v", err)
	}
}

func TestTranslated(t *testing.T) {
	p := load(t, "")
	p.SetLanguage("zh-Hans")
	if p.Name() != "本地文件" {
		t.Fatalf("name %q", p.Name())
	}
}
