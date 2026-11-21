package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePins(t *testing.T) {
	dir := t.TempDir()
	for _, target := range []string{"linux_x64", "macos_arm64"} {
		if err := os.WriteFile(filepath.Join(dir, "ffmpeg-"+FFmpegVersion+"-"+target+".zip"), []byte(target), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "libraries.go")
	if err := writePins("v0.1.0", dir, out); err != nil {
		t.Fatal(err)
	}
	code, _ := os.ReadFile(out)
	for _, want := range []string{`const releaseTag = "v0.1.0"`, `"linux_x64":`, `"macos_arm64":`, `const ffmpegVersion = "` + FFmpegVersion + `"`} {
		if !strings.Contains(string(code), want) {
			t.Errorf("no %s in\n%s", want, code)
		}
	}
}
