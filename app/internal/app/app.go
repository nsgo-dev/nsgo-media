// Package app holds the app's pages and components: the sidebar, plugin
// pages, search, the video page and settings.
package app

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/i18n"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/playback"
	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/internal/store"
	"github.com/oneo-me/nsgo-media/app/locales"
	"github.com/oneo-me/nsgo-media/app/plugins/local"
)

// Version is the app's version, which plugins' minAppVersion is checked
// against.
const Version = "0.1.0"

// App is the main window: the sidebar of the plugins beside the content,
// and the player over both while a source plays. Files dropped anywhere
// play.
type App struct {
	// Builtin are the plugins embedded in the app; nil is the local
	// files plugin.
	Builtin []Builtin
	// DataDir holds the app's data; "" is the platform's (Scope.DataDir).
	DataDir string
	// Client sends the app's own requests for plugins' pictures and
	// subtitles; nil is a client with a timeout.
	Client *http.Client

	// started hands tests the player.
	started func(*player)
}

// Root is the installed app.
func Root(cx *core.Scope) core.Node { return App{}.Render(cx) }

// Render implements core.Node.
func (a App) Render(cx *core.Scope) core.Node {
	dir := a.DataDir
	if dir == "" {
		dir = cx.DataDir()
	}
	in := func(name string) string {
		if dir == "" {
			return ""
		}
		return filepath.Join(dir, name)
	}
	settings, err := store.OpenSettings(in("settings.json"))
	if err != nil {
		log.Printf("settings: %v", err)
	}
	if lang := settings.Language(); lang != "" {
		cx.SetLocale(lang)
	}
	positions, err := store.OpenPositions(in("positions.json"))
	if err != nil {
		log.Printf("resume positions: %v", err)
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	sh := &shell{
		cx:       cx,
		ctx:      cx.Context(),
		settings: settings,
		pictures: newPictures(client),
		prompts:  &prompts{cx: cx, shown: core.NewSignal[*prompt](cx, nil)},
		stack:    core.NewSignalFunc(cx, []route{{kind: routeOpen}}, nil),
		toaster:  ui.NewToaster(cx),
		accounts: map[*plugin.Plugin]*account{},
	}
	builtin := a.Builtin
	if builtin == nil {
		builtin = []Builtin{local.Load}
	}
	sh.library = &library{
		builtin:  builtin,
		dir:      in("plugins"),
		settings: settings,
		opts:     plugin.Options{AppVersion: Version, Language: i18n.Language(cx), DataDir: in("plugin-data"), UI: sh.prompts},
	}
	plugins, err := sh.library.load()
	if err != nil {
		log.Printf("plugins: %v", err)
	}
	sh.plugins = core.NewSignalFunc(cx, plugins, nil)
	sh.toggled = core.NewSignal(cx, 0)
	sh.installing = core.NewSignal[*candidate](cx, nil)
	sh.removing = core.NewSignal[*plugin.Plugin](cx, nil)
	cx.Cleanup(func() {
		for _, p := range sh.plugins.GetUntracked() {
			go p.Close()
		}
	})

	pl := newPlayer(sh, positions, client)
	if a.started != nil {
		a.started(pl)
	}
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1},
		OnFileDrop: func(uris []string) {
			if len(uris) == 0 {
				return
			}
			if f, err := core.FileFromURL(uris[0]); err == nil && f.Path != "" {
				pl.playLocal(f.Path)
			}
		},
		Children: []core.Node{
			ui.SidebarShell{
				TestID:  "shell",
				Fixed:   true,
				Sidebar: []core.Node{sh.sidebar(cx)},
				Header:  sh.header(cx),
				Content: []core.Node{sh.content(cx, pl)},
			},
			pl.page(cx),
			sh.signInDialog(cx),
			sh.installDialog(cx),
			sh.removeDialog(cx),
			ui.Toasts{Toaster: sh.toaster, TestID: "toasts"},
		},
	}
}

// shell is what the pages share: the plugins, the navigation, pictures,
// accounts and toasts.
type shell struct {
	cx       *core.Scope
	ctx      context.Context
	settings *store.Settings
	library  *library
	// plugins are the plugins loaded, enabled or not; the settings say
	// which are, and toggled counts their changes.
	plugins *core.Signal[[]*plugin.Plugin]
	toggled *core.Signal[int]
	// installing is the plugin whose permissions the viewer is asked to
	// approve, and removing the one they are asked to remove.
	installing *core.Signal[*candidate]
	removing   *core.Signal[*plugin.Plugin]
	pictures   *pictures
	prompts    *prompts
	accounts   map[*plugin.Plugin]*account
	toaster    *ui.Toaster
	// stack is the pages opened from the sidebar's entry, the last
	// showing; opened counts the pages opened.
	stack  *core.Signal[[]route]
	opened int
	// play plays a part of a video at a quality, starting at a position.
	play func(p *plugin.Plugin, v *plugin.Video, part int)
}

type routeKind int

const (
	routeOpen routeKind = iota
	routeHome
	routeBrowse
	routeSearch
	routeVideo
	routeSettings
)

// route is a page: the open page, or one of a plugin's.
type route struct {
	// n tells the pages opened apart.
	n      int
	kind   routeKind
	plugin *plugin.Plugin
	// id is browse's or video's, and title what the header shows.
	id, title string
}

// entry is the sidebar item a route of the sidebar is.
func (r route) entry() string {
	switch r.kind {
	case routeOpen:
		return "open"
	case routeSettings:
		return "settings"
	case routeHome:
		return r.plugin.Manifest.ID + "/home"
	case routeSearch:
		return r.plugin.Manifest.ID + "/search"
	}
	return r.plugin.Manifest.ID + "/browse/" + r.id
}

func (sh *shell) top() route {
	s := sh.stack.Get()
	return s[len(s)-1]
}

// open shows r over the page that shows, which stays as it was for back.
func (sh *shell) open(r route) {
	sh.opened++
	r.n = sh.opened
	sh.stack.Update(func(s []route) []route { return append(s[:len(s):len(s)], r) })
}

func (sh *shell) back() {
	sh.stack.Update(func(s []route) []route {
		if len(s) > 1 {
			return s[:len(s)-1]
		}
		return s
	})
}

// openItem opens what an item of p's names.
func (sh *shell) openItem(p *plugin.Plugin, it plugin.Item) {
	if it.Kind == plugin.KindVideo {
		sh.open(route{kind: routeVideo, plugin: p, id: it.ID, title: it.Title})
		return
	}
	sh.open(route{kind: routeBrowse, plugin: p, id: it.ID, title: it.Title})
}

// enabled reports whether the viewer enabled p, following the changes.
func (sh *shell) enabled(p *plugin.Plugin) bool {
	sh.toggled.Get()
	return sh.settings.Enabled(p.Manifest.ID)
}

// active are the plugins enabled.
func (sh *shell) active() []*plugin.Plugin {
	var ps []*plugin.Plugin
	for _, p := range sh.plugins.Get() {
		if sh.enabled(p) {
			ps = append(ps, p)
		}
	}
	return ps
}

// entries are the sidebar's routes: the open page, settings, and each
// enabled plugin's home, search and own entries.
func (sh *shell) entries(cx *core.Scope) []ui.SidebarSection {
	sections := []ui.SidebarSection{{Items: []ui.SidebarItem{
		{ID: "open", Text: locales.OpenPage(cx), Icon: lucide.FolderOpen, TestID: "nav-open"},
		{ID: "settings", Text: locales.Settings(cx), Icon: lucide.Settings, TestID: "nav-settings"},
	}}}
	for _, p := range sh.active() {
		var items []ui.SidebarItem
		id := p.Manifest.ID
		if p.Has(plugin.ExportHome) {
			items = append(items, ui.SidebarItem{ID: id + "/home", Text: locales.Home(cx), Icon: lucide.House, TestID: "nav-" + id + "-home"})
		}
		if p.Has(plugin.ExportSearch) {
			items = append(items, ui.SidebarItem{ID: id + "/search", Text: locales.Search(cx), Icon: lucide.Search, TestID: "nav-" + id + "-search"})
		}
		if p.Has(plugin.ExportBrowse) {
			for _, e := range p.Manifest.Navigation {
				items = append(items, ui.SidebarItem{ID: id + "/browse/" + e.ID, Text: p.Translate(e.Label), Icon: lucide.List, TestID: "nav-" + id + "-" + e.ID})
			}
		}
		sections = append(sections, ui.SidebarSection{
			Title:   p.Name(),
			Items:   items,
			Actions: sh.accountMenu(cx, p),
			TestID:  "plugin-" + id,
		})
	}
	return sections
}

// routeOf is the route of a sidebar entry.
func (sh *shell) routeOf(cx *core.Scope, entry string) (route, bool) {
	switch entry {
	case "open":
		return route{kind: routeOpen}, true
	case "settings":
		return route{kind: routeSettings, title: locales.Settings(cx)}, true
	}
	for _, p := range core.Untracked(cx, sh.active) {
		rest, ok := strings.CutPrefix(entry, p.Manifest.ID+"/")
		if !ok {
			continue
		}
		switch {
		case rest == "home":
			return route{kind: routeHome, plugin: p, title: p.Name()}, true
		case rest == "search":
			return route{kind: routeSearch, plugin: p, title: locales.Search(cx)}, true
		}
		if id, ok := strings.CutPrefix(rest, "browse/"); ok {
			for _, e := range p.Manifest.Navigation {
				if e.ID == id {
					return route{kind: routeBrowse, plugin: p, id: id, title: p.Translate(e.Label)}, true
				}
			}
		}
	}
	return route{}, false
}

func (sh *shell) sidebar(cx *core.Scope) core.Node {
	return ui.Sidebar{
		TestID:       "nav",
		SectionsFunc: func() []ui.SidebarSection { return sh.entries(cx) },
		Selected:     func() string { return sh.stack.Get()[0].entry() },
		OnSelect: func(id string) {
			if r, ok := sh.routeOf(cx, id); ok {
				sh.opened++
				r.n = sh.opened
				sh.stack.Set([]route{r})
			}
		},
	}
}

// header is the content's: back to the page before, and the page's
// title.
func (sh *shell) header(cx *core.Scope) []core.Node {
	return []core.Node{
		core.Show{
			When: func() bool { return len(sh.stack.Get()) > 1 },
			Then: func(cx *core.Scope) core.Node {
				return ui.IconButton{TestID: "page-back", Icon: lucide.ArrowLeft, Label: locales.Back(cx), Variant: ui.IconButtonGhost, OnPress: sh.back}
			},
		},
		core.Text{TestID: "page-title", ContentFunc: func() string { return sh.top().title }, MaxLines: 1, Style: core.Style{FlexShrink: 1, FontWeight: core.FontWeightSemibold}},
	}
}

// content shows the page on top of the stack, keeping those under it.
func (sh *shell) content(cx *core.Scope, pl *player) core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1},
		Children: []core.Node{core.Each(sh.stack.Get, func(r route) int { return r.n }, func(cx *core.Scope, item func() route, index func() int) core.Node {
			return core.View{
				StyleFunc: func() core.Style {
					if index() == len(sh.stack.Get())-1 {
						return core.Style{FlexGrow: 1, FlexShrink: 1}
					}
					return core.Style{Display: core.DisplayNone}
				},
				Children: []core.Node{sh.page(cx, pl, item())},
			}
		})},
	}
}

// page is what a route shows.
func (sh *shell) page(cx *core.Scope, pl *player, r route) core.Node {
	switch r.kind {
	case routeHome:
		return sh.sectionsPage(cx, r.plugin, func(ctx context.Context, cursor string) (*plugin.Page, error) {
			return r.plugin.Home(ctx, plugin.PageParams{Cursor: cursor})
		})
	case routeBrowse:
		return sh.sectionsPage(cx, r.plugin, func(ctx context.Context, cursor string) (*plugin.Page, error) {
			return r.plugin.Browse(ctx, plugin.BrowseParams{ID: r.id, Cursor: cursor})
		})
	case routeSearch:
		return sh.searchPage(cx, r.plugin)
	case routeVideo:
		return sh.videoPage(cx, r.plugin, r.id)
	case routeSettings:
		return sh.settingsPage(cx)
	}
	return openPage(cx, pl.playLocal)
}

// openPage opens a local file or a pasted URL.
func openPage(cx *core.Scope, play func(string)) core.Node {
	url := core.NewSignal(cx, "")
	submit := func() {
		if s := strings.TrimSpace(url.Get()); s != "" {
			play(s)
		}
	}
	return core.View{
		TestID: "page",
		Style:  core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 12, Padding: core.All(24)},
		Children: []core.Node{
			ui.Button{Text: locales.OpenFile(cx), Icon: lucide.FolderOpen, TestID: "open-file", OnPress: func() {
				cx.PickFiles(core.PickFilesOptions{}, func(files []core.File, err error) {
					if err == nil && len(files) > 0 && files[0].Path != "" {
						play(files[0].Path)
					}
				})
			}},
			core.View{
				Style: core.Style{Direction: core.Row, Gap: 8, Width: core.Pct(100), MaxWidth: core.Pt(520)},
				Children: []core.Node{
					ui.Input{
						TestID:      "url",
						Label:       locales.Url(cx),
						Placeholder: locales.UrlPlaceholder(cx),
						Value:       url.Get,
						OnChange:    url.Set,
						OnSubmit:    func(string) { submit() },
						Style:       core.Style{FlexGrow: 1, FlexShrink: 1},
					},
					ui.Button{TestID: "open-url", Text: locales.Open(cx), Variant: ui.ButtonSecondary, OnPress: submit},
				},
			},
		},
	}
}

// player is the player page over the window and what it plays: a local
// source, or a plugin's part, whose qualities, next part and progress go
// back to the plugin.
type player struct {
	sh        *shell
	positions *store.Positions
	client    *http.Client
	playing   *core.Signal[*playback.Source]
	// now is the part playing, nil for a local source.
	now *part
	// parts are the parts played by their sources' keys, to report
	// positions to.
	parts map[string]*part
}

// part is a part of a plugin's video at a quality.
type part struct {
	p       *plugin.Plugin
	video   *plugin.Video
	index   int
	quality string
}

func newPlayer(sh *shell, positions *store.Positions, client *http.Client) *player {
	pl := &player{sh: sh, positions: positions, client: client, playing: core.NewSignal[*playback.Source](sh.cx, nil), parts: map[string]*part{}}
	sh.play = func(p *plugin.Plugin, v *plugin.Video, i int) { pl.playPart(&part{p: p, video: v, index: i}, 0) }
	return pl
}

func (pl *player) playLocal(path string) {
	pl.now = nil
	pl.playing.Set(&playback.Source{URL: path})
}

// playPart calls stream for pt and plays its answer from start.
func (pl *player) playPart(pt *part, start time.Duration) {
	cx := pl.sh.cx
	params := plugin.StreamParams{Video: pt.video.ID, Part: pt.video.Parts[pt.index].ID, Quality: pt.quality}
	call(cx, pl.sh.ctx, func(ctx context.Context) (*plugin.Stream, error) { return pt.p.Stream(ctx, params) }, func(s *plugin.Stream, err error) {
		if err != nil {
			pl.sh.fail(cx, pt.p, err)
			return
		}
		cur := *pt
		cur.quality = s.Quality
		src := sourceOf(partKey(pt.p, pt.video, pt.index), partTitle(cx, pt.video, pt.index), s)
		src.Start = start
		pl.now = &cur
		pl.parts[src.Key] = &cur
		pl.playing.Set(&src)
	})
}

func partKey(p *plugin.Plugin, v *plugin.Video, i int) string {
	return p.Manifest.ID + ":" + v.ID + ":" + v.Parts[i].ID
}

func partTitle(cx *core.Scope, v *plugin.Video, i int) string {
	if len(v.Parts) == 1 {
		return v.Title
	}
	return locales.PartTitle(cx, v.Title, v.Parts[i].Title)
}

// sourceOf is what the player page plays for a stream.
func sourceOf(key, title string, s *plugin.Stream) playback.Source {
	src := playback.Source{Key: key, Title: title, Quality: s.Quality}
	main := s.Video
	if main == nil {
		main = s.Audio
	} else if s.Audio != nil {
		src.Audio = &playback.Stream{URL: localPath(s.Audio.URL), Headers: headers(s.Audio.Headers)}
	}
	src.URL, src.Headers = localPath(main.URL), headers(main.Headers)
	for _, q := range s.Qualities {
		src.Qualities = append(src.Qualities, playback.Quality{ID: q.ID, Label: q.Label})
	}
	for _, sub := range s.Subtitles {
		src.Subtitles = append(src.Subtitles, playback.Subtitle{Label: sub.Label, Text: sub.Text, Stream: playback.Stream{URL: localPath(sub.URL), Headers: headers(sub.Headers)}})
	}
	return src
}

// localPath is the path of a file URL, which the player opens as a path,
// or any other URL as it is.
func localPath(url string) string {
	if path, ok := plugin.FilePath(url); ok {
		return path
	}
	return url
}

func headers(m map[string]string) http.Header {
	if len(m) == 0 {
		return nil
	}
	h := http.Header{}
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

func (pl *player) page(cx *core.Scope) core.Node {
	return core.Show{
		When: func() bool { return pl.playing.Get() != nil },
		Then: func(cx *core.Scope) core.Node {
			// The last source stays while the page closes.
			last := *pl.playing.GetUntracked()
			return playback.Page{
				Source: func() playback.Source {
					if s := pl.playing.Get(); s != nil {
						last = *s
					}
					return last
				},
				Positions: pl.positions,
				Client:    pl.client,
				OnClose: func() {
					pl.now = nil
					pl.playing.Set(nil)
				},
				OnQuality:  pl.switchQuality,
				OnEnded:    pl.next,
				OnPosition: pl.report,
			}
		},
	}
}

// switchQuality plays the part playing at quality id from at.
func (pl *player) switchQuality(id string, at time.Duration) {
	if pl.now != nil {
		pt := *pl.now
		pt.quality = id
		pl.playPart(&pt, at)
	}
}

// next plays the part after the one that ended, at its quality.
func (pl *player) next() {
	if pt := pl.now; pt != nil && pt.index+1 < len(pt.video.Parts) {
		next := *pt
		next.index++
		pl.playPart(&next, 0)
	}
}

// report tells the plugin of the part with key how far the viewer got.
func (pl *player) report(key string, pos, dur time.Duration) {
	pt := pl.parts[key]
	if pt == nil || !pt.p.Has(plugin.ExportProgress) {
		return
	}
	params := plugin.ProgressParams{Video: pt.video.ID, Part: pt.video.Parts[pt.index].ID, Position: pos.Seconds(), Duration: dur.Seconds()}
	go func() {
		if err := pt.p.Progress(context.Background(), params); err != nil {
			log.Printf("progress: %v", err)
		}
	}()
}
