// Package local is the built-in plugin of the viewer's own video folders,
// written in Go as no sandboxed plugin reaches the file system.
package local

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
)

// FS holds the plugin's folder: its manifest and locales.
//
//go:embed plugin.yaml icon.svg locales
var FS embed.FS

// Loader loads the plugin, which lists videos, the system's videos
// folder, while its settings name no folder; "" lists none then.
func Loader(videos string) func(plugin.Options) (*plugin.Plugin, error) {
	l := folders{videos: videos}
	exports := plugin.Native{
		plugin.ExportHome:    l.home,
		plugin.ExportBrowse:  browse,
		plugin.ExportSearch:  l.search,
		plugin.ExportResolve: resolve,
		plugin.ExportVideo:   video,
		plugin.ExportStream:  stream,
	}
	return func(opts plugin.Options) (*plugin.Plugin, error) { return plugin.LoadNative(FS, exports, opts) }
}

// folders finds the viewer's video folders.
type folders struct{ videos string }

const (
	// homeItems bounds a folder's section on the home page, whose rest
	// its own page shows.
	homeItems = 24
	// pageItems is how many of a folder's entries a page shows.
	pageItems = 120
	// searchResults and searchEntries bound a search's results and the
	// entries it reads.
	searchResults = 200
	searchEntries = 50000
	// relatedItems bounds the other videos of a video's folder.
	relatedItems = 50
)

// videoExts are the extensions listed as videos; others are left out.
var videoExts = map[string]bool{
	".3gp": true, ".asf": true, ".avi": true, ".flv": true, ".m2ts": true, ".m4v": true,
	".mkv": true, ".mov": true, ".mp4": true, ".mpeg": true, ".mpg": true, ".mts": true,
	".ogv": true, ".rm": true, ".rmvb": true, ".ts": true, ".vob": true, ".webm": true, ".wmv": true,
}

// subtitleExts are the subtitle files found beside a video.
var subtitleExts = []string{".srt", ".vtt", ".ass", ".ssa"}

func isVideo(name string) bool { return videoExts[strings.ToLower(filepath.Ext(name))] }

func fail(p *plugin.Plugin, code, message string) error {
	return &plugin.Error{Code: code, Message: p.Translate(message)}
}

// of are the viewer's video folders that exist: the setting's, separated
// by semicolons or the system's list separator, else the system's videos
// folder.
func (l folders) of(p *plugin.Plugin) ([]string, error) {
	v, _ := p.Setting("folders")
	text, _ := v.(string)
	var dirs []string
	for _, d := range strings.FieldsFunc(text, func(r rune) bool { return r == ';' || r == '\n' || r == filepath.ListSeparator }) {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 && l.videos != "" {
		dirs = append(dirs, l.videos)
	}
	var found []string
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			found = append(found, filepath.Clean(d))
		}
	}
	if len(found) == 0 {
		return nil, fail(p, plugin.CodeSetup, "Add your video folders in the settings")
	}
	return found, nil
}

func folderItem(path string) plugin.Item {
	return plugin.Item{Kind: plugin.KindCollection, ID: path, Title: filepath.Base(path)}
}

func videoItem(path string) plugin.Item {
	return plugin.Item{Kind: plugin.KindVideo, ID: path, Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
}

// list is dir's folders, then its videos, each by name, leaving out
// hidden ones.
func list(dir string) ([]plugin.Item, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var dirs, videos []plugin.Item
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			isDir = info.IsDir()
		}
		switch {
		case isDir:
			dirs = append(dirs, folderItem(path))
		case isVideo(name):
			videos = append(videos, videoItem(path))
		}
	}
	byName := func(a, b plugin.Item) int { return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)) }
	slices.SortFunc(dirs, byName)
	slices.SortFunc(videos, byName)
	return append(dirs, videos...), nil
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	return v, err
}

// home is a section of each folder.
func (l folders) home(_ context.Context, p *plugin.Plugin, _ json.RawMessage) (any, error) {
	dirs, err := l.of(p)
	if err != nil {
		return nil, err
	}
	pg := plugin.Page{Sections: []plugin.Section{}}
	for _, d := range dirs {
		items, err := list(d)
		if err != nil {
			return nil, err
		}
		s := plugin.Section{Title: filepath.Base(d), Items: items}
		if len(items) > homeItems {
			s.Items, s.More = items[:homeItems], d
		}
		pg.Sections = append(pg.Sections, s)
	}
	return pg, nil
}

// browse is a folder's page of entries, from the cursor's offset.
func browse(_ context.Context, p *plugin.Plugin, raw json.RawMessage) (any, error) {
	params, err := decode[plugin.BrowseParams](raw)
	if err != nil {
		return nil, err
	}
	items, err := list(params.ID)
	if err != nil {
		return nil, err
	}
	from, _ := strconv.Atoi(params.Cursor)
	from = min(max(from, 0), len(items))
	to := min(from+pageItems, len(items))
	pg := plugin.Page{Title: filepath.Base(params.ID), Sections: []plugin.Section{{Items: items[from:to]}}}
	if to < len(items) {
		pg.Next = strconv.Itoa(to)
	}
	return pg, nil
}

// search finds the folders and videos whose names hold the query, in
// every folder and those within.
func (l folders) search(ctx context.Context, p *plugin.Plugin, raw json.RawMessage) (any, error) {
	params, err := decode[plugin.SearchParams](raw)
	if err != nil {
		return nil, err
	}
	dirs, err := l.of(p)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(params.Query))
	pg := plugin.Page{Sections: []plugin.Section{}}
	if query == "" {
		return pg, nil
	}
	var found, videos []plugin.Item
	seen, read := map[string]bool{}, 0
	done := func() bool {
		return len(found)+len(videos) >= searchResults || read >= searchEntries || ctx.Err() != nil
	}
	for _, root := range dirs {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if done() {
				return fs.SkipAll
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			read++
			name := d.Name()
			if path != root && strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if seen[path] {
					return fs.SkipDir // a folder within another of the setting's
				}
				seen[path] = true
			}
			if path == root || !strings.Contains(strings.ToLower(name), query) {
				return nil
			}
			switch {
			case d.IsDir() && params.Kind != plugin.KindVideo:
				found = append(found, folderItem(path))
			case !d.IsDir() && isVideo(name) && (params.Kind == "" || params.Kind == plugin.KindVideo):
				videos = append(videos, videoItem(path))
			}
			return nil
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(found) > 0 {
		pg.Sections = append(pg.Sections, plugin.Section{Title: p.Translate("Folders"), Items: found})
	}
	if len(videos) > 0 {
		pg.Sections = append(pg.Sections, plugin.Section{Title: p.Translate("Videos"), Items: videos})
	}
	return pg, nil
}

// resolve turns a pasted path or file URL of a folder or a video into its
// item.
func resolve(_ context.Context, _ *plugin.Plugin, raw json.RawMessage) (any, error) {
	params, err := decode[plugin.ResolveParams](raw)
	if err != nil {
		return nil, err
	}
	path := strings.Trim(strings.TrimSpace(params.Text), `"'`)
	if f, ok := plugin.FilePath(path); ok {
		path = f
	}
	if !filepath.IsAbs(path) {
		return nil, nil
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, nil
	case info.IsDir():
		return folderItem(filepath.Clean(path)), nil
	case isVideo(path):
		return videoItem(filepath.Clean(path)), nil
	}
	return nil, nil
}

// file is the video file of id, a path.
func file(p *plugin.Plugin, id string) (string, error) {
	if info, err := os.Stat(id); err != nil || !info.Mode().IsRegular() {
		return "", fail(p, plugin.CodeUnavailable, "This file is not available")
	}
	return id, nil
}

// video is a file as one part, with the other videos of its folder.
func video(_ context.Context, p *plugin.Plugin, raw json.RawMessage) (any, error) {
	params, err := decode[plugin.VideoParams](raw)
	if err != nil {
		return nil, err
	}
	path, err := file(p, params.ID)
	if err != nil {
		return nil, err
	}
	it := videoItem(path)
	v := plugin.Video{ID: path, Title: it.Title, Description: filepath.Dir(path), Parts: []plugin.Part{{ID: "file", Title: it.Title}}}
	siblings, _ := list(filepath.Dir(path))
	for _, s := range siblings {
		if s.Kind == plugin.KindVideo && s.ID != path && len(v.Related) < relatedItems {
			v.Related = append(v.Related, s)
		}
	}
	return v, nil
}

// stream plays the file with the subtitle files beside it: those named
// as it is, or as it is followed by a language or a label.
func stream(_ context.Context, p *plugin.Plugin, raw json.RawMessage) (any, error) {
	params, err := decode[plugin.StreamParams](raw)
	if err != nil {
		return nil, err
	}
	path, err := file(p, params.Video)
	if err != nil {
		return nil, err
	}
	s := plugin.Stream{Video: &plugin.Source{URL: plugin.FileURL(path)}}
	dir, base := filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if e.IsDir() || !slices.Contains(subtitleExts, ext) {
			continue
		}
		label, ok := strings.CutPrefix(stem, base)
		if !ok || (label != "" && label[0] != '.') {
			continue
		}
		label = strings.TrimPrefix(label, ".")
		if label == "" {
			label = strings.ToUpper(ext[1:])
		}
		s.Subtitles = append(s.Subtitles, plugin.Subtitle{Label: label, Source: plugin.Source{URL: plugin.FileURL(filepath.Join(dir, name))}})
	}
	return s, nil
}
