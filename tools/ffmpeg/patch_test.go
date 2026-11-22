package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnifiedDiffApplies checks that patch(1) turns the original into the
// changed text with the diff ffmpegDiff ships.
func TestUnifiedDiffApplies(t *testing.T) {
	if _, err := exec.LookPath("patch"); err != nil {
		t.Skip("no patch command")
	}
	var lines []string
	for i := range 20 {
		lines = append(lines, "line "+string(rune('a'+i)))
	}
	p := ffmpegPatches[0]
	for name, orig := range map[string]string{
		"middle": strings.Join(lines[:10], "\n") + "\n" + p.old + " args)\n" + strings.Join(lines[10:], "\n") + "\n",
		"start":  p.old + " args)\n" + strings.Join(lines[:2], "\n") + "\n",
		"end":    strings.Join(lines[:1], "\n") + "\n" + p.old + " args)\n",
	} {
		t.Run(name, func(t *testing.T) {
			diff, err := unifiedDiff(p.file, orig, p.old, p.new)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, p.file)
			os.MkdirAll(filepath.Dir(path), 0o755)
			os.WriteFile(path, []byte(orig), 0o644)
			cmd := exec.Command("patch", "-p1", "--quiet")
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader("Changed by NSGo Media on " + p.date + ": " + p.why + "\n" + diff)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("patch: %v\n%s\n%s", err, out, diff)
			}
			got, _ := os.ReadFile(path)
			if want := strings.Replace(orig, p.old, p.new, 1); string(got) != want {
				t.Errorf("patched to\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestUnifiedDiffNoLongerApplies(t *testing.T) {
	if _, err := unifiedDiff("f.c", "nothing here\n", "absent", "x"); err == nil {
		t.Error("no error for a patch that does not apply")
	}
}
