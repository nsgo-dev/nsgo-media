package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRecentKeepsTheNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")
	r, err := OpenRecent(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"a", "b", "a"} {
		if err := r.Add("demo", v, json.RawMessage(`{"id":"`+v+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range maxRecent {
		if err := r.Add("other", string(rune('A'+i)), json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	again, err := OpenRecent(path)
	if err != nil {
		t.Fatal(err)
	}
	list := again.List()
	if len(list) != maxRecent || list[0].Plugin != "other" || list[maxRecent-1].Video == "a" {
		t.Fatalf("recent = %+v", list)
	}
}

func TestDanmakuDefaults(t *testing.T) {
	var d Danmaku
	if d.Opacity() != 0.85 || d.Quarters() != 2 {
		t.Fatalf("defaults: opacity %v, quarters %d", d.Opacity(), d.Quarters())
	}
	d.SetOpacity(0)
	path := filepath.Join(t.TempDir(), "settings.json")
	s, _ := OpenSettings(path)
	if err := s.SetDanmaku(d); err != nil {
		t.Fatal(err)
	}
	s, _ = OpenSettings(path)
	if o := s.Danmaku().Opacity(); o < 0.099 || o > 0.101 {
		t.Fatalf("opacity %v", o)
	}
}
