package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// patchName is the file holding ffmpegPatches as a unified diff, in each
// archive and beside FFmpeg's source in the output, as the LGPL asks of a
// changed library.
const patchName = "ffmpeg.patch"

// ffmpegDiff is ffmpegPatches as a unified diff of the unpacked source,
// each change headed by who changed the file, when and why.
func (b *builder) ffmpegDiff() (string, error) {
	var out strings.Builder
	for _, p := range ffmpegPatches {
		data, err := os.ReadFile(filepath.Join(b.ffmpeg, p.file))
		if err != nil {
			return "", err
		}
		orig := strings.Replace(string(data), p.new, p.old, 1)
		hunk, err := unifiedDiff(p.file, orig, p.old, p.new)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "Changed by NSGo Media on %s: %s\n%s", p.date, p.why, hunk)
	}
	return out.String(), nil
}

// unifiedDiff is the diff of file, whose text is orig, replacing old with
// new once, with three lines of context.
func unifiedDiff(file, orig, old, new string) (string, error) {
	i := strings.Index(orig, old)
	if i < 0 {
		return "", fmt.Errorf("%s: the patch no longer applies", file)
	}
	// The changed lines are the whole lines old touches.
	start := strings.LastIndexByte(orig[:i], '\n') + 1
	end := i + len(old)
	if j := strings.IndexByte(orig[end:], '\n'); j >= 0 {
		end += j + 1
	} else {
		end = len(orig)
	}
	lines := strings.SplitAfter(orig, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	first := strings.Count(orig[:start], "\n")
	oldLines := strings.SplitAfter(strings.TrimSuffix(orig[start:end], "\n"), "\n")
	newText := orig[start:i] + new + orig[i+len(old):end]
	newLines := strings.SplitAfter(strings.TrimSuffix(newText, "\n"), "\n")
	last := first + len(oldLines)

	// Lines the change keeps at its ends are context too.
	pre := 0
	for pre < len(oldLines) && pre < len(newLines) && oldLines[pre] == newLines[pre] {
		pre++
	}
	post := 0
	for post < len(oldLines)-pre && post < len(newLines)-pre && oldLines[len(oldLines)-1-post] == newLines[len(newLines)-1-post] {
		post++
	}
	from, to := max(0, first-3), min(len(lines), last+3)

	var body strings.Builder
	line := func(mark byte, s string) {
		body.WriteByte(mark)
		body.WriteString(strings.TrimSuffix(s, "\n"))
		body.WriteByte('\n')
	}
	for _, s := range lines[from:first] {
		line(' ', s)
	}
	for _, s := range oldLines[:pre] {
		line(' ', s)
	}
	for _, s := range oldLines[pre : len(oldLines)-post] {
		line('-', s)
	}
	for _, s := range newLines[pre : len(newLines)-post] {
		line('+', s)
	}
	for _, s := range oldLines[len(oldLines)-post:] {
		line(' ', s)
	}
	for _, s := range lines[last:to] {
		line(' ', s)
	}
	oldCount := to - from
	newCount := oldCount - len(oldLines) + len(newLines)
	return fmt.Sprintf("--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n%s", file, file, from+1, oldCount, from+1, newCount, body.String()), nil
}

// writeSource puts FFmpeg's source as it was downloaded and the diff of
// ffmpegPatches into out/source, which an app offers beside its downloads
// as the source of the libraries it ships.
func (b *builder) writeSource() error {
	dir := filepath.Join(b.out, "source")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tarball, err := b.fetch(ffmpegSource)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(tarball)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ffmpegSource.name), data, 0o644); err != nil {
		return err
	}
	diff, err := b.ffmpegDiff()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, patchName), []byte(diff), 0o644)
}
