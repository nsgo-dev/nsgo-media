package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPositionsOutliveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "positions.json")
	p, err := OpenPositions(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Get("a"); ok {
		t.Fatal("a new store has a position")
	}
	if err := p.Set("a", 90*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("b", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove("b"); err != nil {
		t.Fatal(err)
	}

	again, err := OpenPositions(path)
	if err != nil {
		t.Fatal(err)
	}
	if pos, ok := again.Get("a"); !ok || pos != 90*time.Second {
		t.Fatalf("a = %v, %v; want 1m30s", pos, ok)
	}
	if _, ok := again.Get("b"); ok {
		t.Fatal("b is kept after Remove")
	}
}

func TestPositionsReplaceAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "positions.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPositions(path)
	if err == nil {
		t.Fatal("no error for a broken file")
	}
	if err := p.Set("a", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPositions(path); err != nil {
		t.Fatalf("the file is still broken: %v", err)
	}
}

func TestPositionsForgetTheLeastRecentlyPlayed(t *testing.T) {
	p, _ := OpenPositions(filepath.Join(t.TempDir(), "positions.json"))
	for i := range maxPositions + 1 {
		p.entries[fmt.Sprint(i)] = position{Position: time.Minute, Played: time.Unix(int64(i), 0)}
	}
	p.prune()
	if len(p.entries) != maxPositions {
		t.Fatalf("%d positions; want %d", len(p.entries), maxPositions)
	}
	if _, ok := p.Get("0"); ok {
		t.Fatal("the oldest position is kept")
	}
	if _, ok := p.Get(fmt.Sprint(maxPositions)); !ok {
		t.Fatal("the newest position is forgotten")
	}
}
