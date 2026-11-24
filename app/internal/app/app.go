// Package app holds the app's pages and components: the sidebar, plugin
// pages, search, the video page and settings.
package app

import (
	"context"
	_ "embed"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/i18n"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/playback"
	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/internal/store"
	"github.com/oneo-me/nsgo-media/app/locales"
	"github.com/oneo-me/nsgo-media/app/plugins/local"
	"github.com/oneo-me/nsgo-media/internal/stats"
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
	// report counts the app's use (stats.Report).
	report bool
	// gitHub is the root of GitHub's REST API, which tests stand a
	// server in for; "" is GitHub's.
	gitHub string
}

// Root is the installed app.
func Root(cx *core.Scope) core.Node { return App{report: true}.Render(cx) }

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
	cx.SetTheme(theme())
	sh := &shell{
		cx:        cx,
		ctx:       cx.Context(),
		settings:  settings,
		positions: positions,
		played:    core.NewSignal(cx, 0),
		dragging:  core.NewSignal(cx, false),
		pictures:  newPictures(client),
		prompts:   &prompts{cx: cx, shown: core.NewSignal[*prompt](cx, nil)},
		stack:     core.NewSignalFunc(cx, []route{{kind: routeOpen}}, nil),
		toaster:   ui.NewToaster(cx),
		accounts:  map[*plugin.Plugin]*account{},
	}
	api := a.gitHub
	if api == "" {
		api = gitHubAPI
	}
	sh.gitHub = &gitHub{api: api, client: &http.Client{Timeout: 60 * time.Second}}
	builtin := a.Builtin
	if builtin == nil {
		// No videos folder, as on phones, leaves the folders to the settings.
		videos, _ := desktop.UserDir(cx, desktop.VideosDir)
		builtin = []Builtin{local.Loader(videos)}
	}
	sh.library = &library{
		builtin:  builtin,
		dir:      in("plugins"),
		settings: settings,
		opts:     plugin.Options{AppVersion: Version, Language: i18n.Language(cx), DataDir: in("plugin-data"), UI: sh.prompts},
	}
	plugins, refused, err := sh.library.load()
	if err != nil {
		log.Printf("plugins: %v", err)
	}
	sh.plugins = core.NewSignalFunc(cx, plugins, nil)
	sh.refused = core.NewSignalFunc(cx, refused, nil)
	sh.toggled = core.NewSignal(cx, 0)
	sh.installing = core.NewSignal[*candidate](cx, nil)
	sh.install = core.NewSignal(cx, installState{})
	sh.removing = core.NewSignal[*removal](cx, nil)
	sh.typingRepo = core.NewSignal(cx, false)
	sh.fromGitHub = core.NewSignal(cx, fromGitHubState{})
	sh.updates = core.NewSignalFunc[map[string]*update](cx, nil, nil)
	sh.checking = core.NewSignal(cx, checkState{})
	sh.updating = core.NewSignal[*update](cx, nil)
	sh.updateState = core.NewSignal(cx, installState{})
	cx.Cleanup(func() {
		for _, p := range sh.plugins.GetUntracked() {
			go p.Close()
		}
	})

	pl := newPlayer(sh, positions, client)
	if a.started != nil {
		a.started(pl)
	}
	sh.checkUpdates()
	if a.report {
		// Pages are counted by their kind, never a plugin's own.
		stats.Report(cx, stats.Options{Tag: "app",
			Page: func() (string, string) {
				if pl.playing.Get() != nil {
					return "/app/player", "Player"
				}
				k := statsPages[sh.top().kind]
				return "/app/" + k, k
			},
			On:   settings.Stats,
			Busy: func() bool { return pl.playing.Get() != nil }})
	}
	return core.View{
		Style:           core.Style{FlexGrow: 1, FlexShrink: 1},
		OnFileDragEnter: func() { sh.dragging.Set(true) },
		OnFileDragLeave: func() { sh.dragging.Set(false) },
		OnFileDrop: func(uris []string) {
			sh.dragging.Set(false)
			if len(uris) == 0 {
				return
			}
			if f, err := core.FileFromURL(uris[0]); err == nil && f.Path != "" {
				pl.playLocal(f.Path)
			}
		},
		Children: []core.Node{
			ui.SidebarShell{
				TestID:        "shell",
				Fixed:         true,
				DefaultWidth:  232,
				Sidebar:       []core.Node{sh.sidebar(cx)},
				SidebarFooter: []core.Node{sh.sidebarFooter(cx)},
				Header:        sh.header(cx),
				Content:       []core.Node{sh.content(cx, pl)},
			},
			pl.page(cx),
			sh.signInDialog(cx),
			sh.fromGitHubDialog(cx),
			sh.installDialog(cx),
			sh.updateDialog(cx),
			sh.removeDialog(cx),
			ui.Toasts{Toaster: sh.toaster, TestID: "toasts"},
		},
	}
}

// shell is what the pages share: the plugins, the navigation, pictures,
// accounts and toasts.
type shell struct {
	cx        *core.Scope
	ctx       context.Context
	settings  *store.Settings
	positions *store.Positions
	// played counts the player closing, after which the resume positions
	// the pages show change.
	played *core.Signal[int]
	// dragging is a file dragged over the window.
	dragging *core.Signal[bool]
	library  *library
	// plugins are the plugins loaded, enabled or not; the settings say
	// which are, and toggled counts their changes. refused are those the
	// app refused to load.
	plugins *core.Signal[[]*plugin.Plugin]
	refused *core.Signal[[]*refusal]
	toggled *core.Signal[int]
	// installing is the plugin whose permissions the viewer is asked to
	// approve, install how installing it goes, and removing the plugin
	// they are asked to remove.
	installing *core.Signal[*candidate]
	install    *core.Signal[installState]
	removing   *core.Signal[*removal]
	// gitHub finds plugins' releases; typingRepo is the viewer typing a
	// repository to install from, and fromGitHub how finding it goes.
	gitHub     *gitHub
	typingRepo *core.Signal[bool]
	fromGitHub *core.Signal[fromGitHubState]
	// updates are the newer releases of the plugins installed from
	// GitHub, by the plugins' ids, and checking how checking for them
	// goes; updating is the one the viewer is asked to install, and
	// updateState how installing it goes.
	updates     *core.Signal[map[string]*update]
	checking    *core.Signal[checkState]
	updating    *core.Signal[*update]
	updateState *core.Signal[installState]
	// replaced tells the player of a plugin loaded in another's place.
	replaced func(old, p *plugin.Plugin)
	pictures *pictures
	prompts  *prompts
	accounts map[*plugin.Plugin]*account
	toaster  *ui.Toaster
	// stack is the pages opened from the sidebar's entry, the last
	// showing; opened counts the pages opened.
	stack  *core.Signal[[]route]
	opened int
	// play plays a part of a video from start, which 0 leaves to the
	// resume offer unless fromStart.
	play func(p *plugin.Plugin, v *plugin.Video, part int, start time.Duration, fromStart bool)
}

type routeKind int

const (
	routeOpen routeKind = iota
	routeHome
	routeBrowse
	routeSearch
	routeVideo
	routeSettings
	routePlugin
	routeFollowing
)

// statsPages name the kinds of page in the app's statistics.
var statsPages = map[routeKind]string{routeOpen: "open", routeHome: "home", routeBrowse: "browse",
	routeSearch: "search", routeVideo: "video", routeSettings: "settings", routePlugin: "plugin", routeFollowing: "following"}

// route is a page: the open page, or one of a plugin's.
type route struct {
	// n tells the pages opened apart.
	n      int
	kind   routeKind
	plugin *plugin.Plugin
	// refused is the plugin a plugin page shows when the app refused it.
	refused *refusal
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
	case routeFollowing:
		return r.plugin.Manifest.ID + "/following"
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

// entries are the sidebar's routes: the open page, and each enabled
// plugin's home, search and own entries; settings are in its footer.
func (sh *shell) entries(cx *core.Scope) []ui.SidebarSection {
	sections := []ui.SidebarSection{{Items: []ui.SidebarItem{
		{ID: "open", Text: locales.OpenPage(cx), Icon: lucide.FolderOpen, TestID: "nav-open"},
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
		if p.Has(plugin.ExportFollowing) {
			items = append(items, ui.SidebarItem{ID: id + "/following", Text: locales.Following(cx), Content: sh.followingEntry(cx, p), TestID: "nav-" + id + ":following"})
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
		case rest == "following":
			return route{kind: routeFollowing, plugin: p, title: locales.Following(cx)}, true
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
		OnSelect:     func(id string) { sh.choose(cx, id) },
	}
}

// sidebarFooter holds the settings, at the sidebar's foot.
func (sh *shell) sidebarFooter(cx *core.Scope) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{ui.Sidebar{
		TestID: "nav-footer",
		SectionsFunc: func() []ui.SidebarSection {
			return []ui.SidebarSection{{Items: []ui.SidebarItem{
				{ID: "settings", Text: locales.Settings(cx), Content: badgedEntry(cx, lucide.Settings, locales.Settings(cx), "updates-badge", func() int { return len(sh.updates.Get()) }), TestID: "nav-settings"},
			}}}
		},
		Selected: func() string { return sh.stack.Get()[0].entry() },
		OnSelect: func(id string) { sh.choose(cx, id) },
	}}}
}

// choose starts over at the sidebar's entry id.
func (sh *shell) choose(cx *core.Scope, id string) {
	if r, ok := sh.routeOf(cx, id); ok {
		sh.opened++
		r.n = sh.opened
		sh.stack.Set([]route{r})
	}
}

// header is the content's: back to the page before, and the page's
// title.
func (sh *shell) header(cx *core.Scope) []core.Node {
	return []core.Node{
		core.Show{
			When: func() bool { return len(sh.stack.Get()) > 1 },
			Then: func(cx *core.Scope) core.Node {
				return ui.IconButton{TestID: "page-back", Icon: lucide.ChevronLeft, Label: locales.Back(cx), Variant: ui.IconButtonGhost, Size: 28, IconSize: 18, OnPress: sh.back}
			},
		},
		core.Text{TestID: "page-title", ContentFunc: func() string { return sh.top().title }, MaxLines: 1, Class: "text-[15px] font-semibold text-foreground", Style: core.Style{FlexShrink: 1}},
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
		return sh.sectionsPage(cx, r.plugin, pageHome, func(ctx context.Context, cursor string) (*plugin.Page, error) {
			return r.plugin.Home(ctx, plugin.PageParams{Cursor: cursor})
		})
	case routeBrowse:
		return sh.sectionsPage(cx, r.plugin, pageBrowse, func(ctx context.Context, cursor string) (*plugin.Page, error) {
			return r.plugin.Browse(ctx, plugin.BrowseParams{ID: r.id, Cursor: cursor})
		})
	case routeSearch:
		return sh.searchPage(cx, r.plugin)
	case routeFollowing:
		return sh.followingPage(cx, r.plugin)
	case routeVideo:
		return sh.videoPage(cx, r.plugin, r.id)
	case routeSettings:
		return sh.settingsPage(cx)
	case routePlugin:
		if r.refused != nil {
			return sh.refusedPage(cx, r.refused)
		}
		return sh.pluginPage(cx, r.plugin)
	}
	return openPage(cx, sh.dragging.Get, pl.playLocal)
}

// appIcon is the app's icon, as the open page shows it.
//
//go:embed appicon.png
var appIcon []byte

// openPage opens a local file or a pasted URL; dragging is a file
// dragged over the window, which plays where it drops.
func openPage(cx *core.Scope, dragging func() bool, play func(string)) core.Node {
	url := core.NewSignal(cx, "")
	submit := func() {
		if s := strings.TrimSpace(url.Get()); s != "" {
			play(s)
		}
	}
	pick := func() {
		cx.PickFiles(core.PickFilesOptions{}, func(files []core.File, err error) {
			if err == nil && len(files) > 0 && files[0].Path != "" {
				play(files[0].Path)
			}
		})
	}
	drop := core.View{
		TestID: "drop",
		ClassFunc: func() string {
			if dragging() {
				return "w-[520px] items-center gap-4 rounded-3xl border-2 border-dashed border-accent bg-accent/[0.07] px-10 py-10"
			}
			return "w-[520px] items-center gap-4 rounded-3xl border-2 border-dashed border-border bg-chip/60 px-10 py-10"
		},
		Children: []core.Node{core.Show{
			When: dragging,
			Then: func(cx *core.Scope) core.Node {
				return core.Group{
					core.View{Class: "size-20 items-center justify-center rounded-3xl bg-accent shadow-lg", Children: []core.Node{
						core.Icon{SVG: lucide.ArrowDownToLine(), Class: "size-9 text-white"},
					}},
					core.Text{Content: locales.DropToPlay(cx), Class: "text-lg font-semibold text-accent-ink"},
					core.View{Class: "h-10"},
				}
			},
			Else: func(cx *core.Scope) core.Node {
				return core.Group{
					core.Image{Source: core.ImageSource{Data: appIcon}, Class: "size-20", Label: locales.AppName(cx)},
					core.View{Class: "items-center gap-1", Children: []core.Node{
						core.Text{Content: locales.DropHere(cx), Class: "text-lg font-semibold text-foreground"},
						core.Text{Content: locales.DropFormats(cx), Class: "text-center text-[13px] text-muted-foreground"},
					}},
					ui.Button{Text: locales.OpenFile(cx), Icon: lucide.FolderOpen, Size: ui.SizeLarge, TestID: "open-file", OnPress: pick},
				}
			},
		}},
	}
	return core.View{
		TestID: "page",
		Class:  "flex-1 items-center justify-center gap-6 px-10 pb-10",
		Children: []core.Node{
			drop,
			core.View{Class: "w-[520px] gap-2", Children: []core.Node{
				core.Text{Content: locales.OrPlayURL(cx), Class: "px-1 text-xs font-semibold text-muted-foreground"},
				core.View{Class: "flex-row gap-2", Children: []core.Node{
					field(lucide.Link, ui.Input{
						TestID:      "url",
						Label:       locales.Url(cx),
						Placeholder: locales.UrlPlaceholder(cx),
						Value:       url.Get,
						OnChange:    url.Set,
						OnSubmit:    func(string) { submit() },
					}),
					ui.Button{TestID: "open-url", Text: locales.Open(cx), Variant: ui.ButtonOutline, Size: ui.SizeLarge, OnPress: submit},
				}},
			}},
		},
	}
}

// field is a large input with an icon at its start, as the open and
// search pages' are; more ends it, such as a spinner.
func field(icon func() *core.SVG, in ui.Input, more ...core.Node) core.Node {
	in.Style = in.Style.Merge(core.Style{Height: core.Pt(40), CornerRadius: 12, FontSize: 14, Padding: core.Edges{Left: 38, Right: 36}, Width: core.Pct(100)})
	children := []core.Node{
		in,
		core.Icon{SVG: icon(), Class: "absolute top-3 left-3.5 size-4 text-faint-ink"},
	}
	for _, m := range more {
		children = append(children, core.View{Class: "absolute top-0 right-3 bottom-0 justify-center", Children: []core.Node{m}})
	}
	return core.View{Class: "flex-1", Style: core.Style{FlexShrink: 1}, Children: children}
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
	// fromStart plays it from its start without offering to resume.
	fromStart bool
}

func newPlayer(sh *shell, positions *store.Positions, client *http.Client) *player {
	pl := &player{sh: sh, positions: positions, client: client, playing: core.NewSignal[*playback.Source](sh.cx, nil), parts: map[string]*part{}}
	sh.play = func(p *plugin.Plugin, v *plugin.Video, i int, start time.Duration, fromStart bool) {
		pl.playPart(&part{p: p, video: v, index: i, fromStart: fromStart}, start)
	}
	// What plays keeps playing; its qualities and parts come from the
	// plugin loaded in its place.
	sh.replaced = func(old, p *plugin.Plugin) {
		for _, pt := range pl.parts {
			if pt.p == old {
				pt.p = p
			}
		}
		if pl.now != nil && pl.now.p == old {
			pl.now.p = p
		}
	}
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
		src.Start, src.FromStart = start, pt.fromStart && start == 0
		cur.fromStart = false
		if len(pt.video.Parts) > 1 {
			src.Video = pt.video.Title
			for i, vp := range pt.video.Parts {
				src.Parts = append(src.Parts, playback.Part{Label: locales.PartNumber(cx, i+1), Title: vp.Title, Duration: seconds(vp.Duration)})
			}
			src.Part = pt.index
		}
		pl.now = &cur
		pl.parts[src.Key] = &cur
		pl.playing.Set(&src)
	})
}

func partKey(p *plugin.Plugin, v *plugin.Video, i int) string {
	return videoKey(p, v.ID) + v.Parts[i].ID
}

// videoKey starts the keys of the parts of p's video id.
func videoKey(p *plugin.Plugin, id string) string { return p.Manifest.ID + ":" + id + ":" }

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

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
					pl.sh.played.Update(func(n int) int { return n + 1 })
				},
				OnQuality:  pl.switchQuality,
				OnPart:     pl.playIndex,
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

// playIndex plays the part at index i of the video playing, at its
// quality, from its start.
func (pl *player) playIndex(i int) {
	if pt := pl.now; pt != nil && i >= 0 && i < len(pt.video.Parts) {
		other := *pt
		other.index = i
		pl.playPart(&other, 0)
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
