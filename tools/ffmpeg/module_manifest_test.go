package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	nsgotargets "github.com/nsgo-dev/nsgo/targets"
)

// TestModuleManifest checks that nsgo-module.yaml's targets are the table of targets in docs/ffmpeg.md, and that its notes fit NSGo's 4 KiB and point at the full list of duties.
func TestModuleManifest(t *testing.T) {
	data, err := os.ReadFile("../../nsgo-module.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(data)

	line := regexp.MustCompile(`(?m)^targets: \[(.*)\]$`).FindStringSubmatch(manifest)
	if line == nil {
		t.Fatal("nsgo-module.yaml has no one-line targets list")
	}
	var listed []string
	for name := range strings.SplitSeq(line[1], ",") {
		listed = append(listed, strings.TrimSpace(name))
	}
	got, err := nsgotargets.Expand(listed)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := os.ReadFile("../../docs/ffmpeg.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(doc), "\n| Target |")
	if !ok {
		t.Fatal("docs/ffmpeg.md has no table of targets")
	}
	var want []string
	for row := range strings.SplitSeq(table, "\n") {
		if row == "" {
			break
		}
		cells := strings.Split(row, "|")
		if len(cells) < 2 {
			break
		}
		for _, m := range regexp.MustCompile("`([a-z0-9_]+)`").FindAllStringSubmatch(cells[1], -1) {
			want = append(want, m[1])
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Errorf("nsgo-module.yaml's targets expand to %v; docs/ffmpeg.md's table lists %v", got, want)
	}

	_, notes, ok := strings.Cut(manifest, "\nnotes: |\n")
	if !ok {
		t.Fatal("nsgo-module.yaml has no notes block")
	}
	var text strings.Builder
	for l := range strings.SplitSeq(notes, "\n") {
		if l != "" && !strings.HasPrefix(l, "  ") {
			t.Fatalf("nsgo-module.yaml has %q after its notes", l)
		}
		text.WriteString(strings.TrimPrefix(l, "  ") + "\n")
	}
	if n := len(strings.TrimRight(text.String(), "\n")) + 1; n > 4<<10 {
		t.Errorf("notes are %d bytes; NSGo takes at most 4 KiB", n)
	}
	if !strings.Contains(text.String(), "docs/ffmpeg.md#what-an-app-that-ships-them-owes") {
		t.Error("notes do not point at docs/ffmpeg.md for the full list of duties")
	}
}
