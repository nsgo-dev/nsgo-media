package playback

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// maxSubtitles bounds a downloaded subtitle file.
const maxSubtitles = 16 << 20

// subtitleFile is the file the player reads sub from: its own path for a
// file, else its text or a download in its own folder of dir named by its
// label, which the player shows as the track's title.
func subtitleFile(ctx context.Context, client *http.Client, dir string, i int, sub Subtitle) (string, error) {
	if sub.Text != "" {
		return writeSubtitles(dir, i, sub.Label, subtitleExt("", "", []byte(sub.Text)), []byte(sub.Text))
	}
	u, err := url.Parse(sub.URL)
	if err != nil || len(u.Scheme) <= 1 {
		return sub.URL, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.URL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range sub.Headers {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subtitles %s: %s", sub.Label, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitles+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSubtitles {
		return "", fmt.Errorf("subtitles %s are larger than %d MB", sub.Label, maxSubtitles>>20)
	}
	return writeSubtitles(dir, i, sub.Label, subtitleExt(u.Path, resp.Header.Get("Content-Type"), data), data)
}

func writeSubtitles(dir string, i int, label, ext string, data []byte) (string, error) {
	folder := filepath.Join(dir, strconv.Itoa(i))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	file := filepath.Join(folder, fileName(label)+ext)
	return file, os.WriteFile(file, data, 0o600)
}

// subtitleExt is the extension the player parses data by: the URL's,
// else the content type's, else what the text starts with.
func subtitleExt(urlPath, contentType string, data []byte) string {
	switch ext := strings.ToLower(path.Ext(urlPath)); ext {
	case ".srt", ".vtt", ".ass", ".ssa":
		return ext
	}
	switch {
	case strings.HasPrefix(contentType, "text/vtt"):
		return ".vtt"
	case strings.Contains(contentType, "ssa") || strings.Contains(contentType, "ass"):
		return ".ass"
	}
	head := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	switch {
	case bytes.HasPrefix(head, []byte("WEBVTT")):
		return ".vtt"
	case bytes.HasPrefix(head, []byte("[Script Info]")):
		return ".ass"
	}
	return ".srt"
}

// fileName is label as a file name on every desktop.
func fileName(label string) string {
	name := strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(label))
	name = strings.Trim(name, ". ")
	if name == "" {
		return "subtitles"
	}
	return name
}
