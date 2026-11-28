// Package app holds the app's pages and components: the sidebar, plugin
// pages, search, the video page and settings.
package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/i18n"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/nsgo-dev/nsgo-media/app/internal/playback"
	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
	"github.com/nsgo-dev/nsgo-media/app/internal/store"
	"github.com/nsgo-dev/nsgo-media/app/locales"
	"github.com/nsgo-dev/nsgo-media/app/plugins/local"
	"github.com/nsgo-dev/nsgo-media/internal/stats"
)

// Version is the app's version, which plugins' minAppVersion is checked
// against.
const Version = "0.1.0"

// App is the main window: the unified toolbar over the pages of every
// enabled plugin together, and a player window of its own while a source
// plays. Files dropped anywhere play.
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
	recent, err := store.OpenRecent(in("recent.json"))
	if err != nil {
		log.Printf("recent videos: %v", err)
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
		recent:    recent,
		played:    core.NewSignal(cx, 0),
		dragging:  core.NewSignal(cx, false),
		pictures:  newPictures(client),
		prompts:   &prompts{cx: cx, shown: core.NewSignal[*prompt](cx, nil)},
		stack:     core.NewSignalFunc(cx, []route{{kind: routeHome}}, nil),
		filter:    core.NewSignal(cx, ""),
		marks:     map[*plugin.Plugin]*core.SVG{},
		searches:  core.NewSignal(cx, 0),
		news:      core.NewSignalFunc[map[*plugin.Plugin]int](cx, nil, nil),
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
			core.View{TestID: "shell", Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{
				sh.toolbar(cx),
				sh.content(cx, pl),
			}},
			sh.newsWatcher(cx),
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
	recent    *store.Recent
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
	// filter is the id of the plugin the merged pages show alone, "" for
	// all of them.
	filter *core.Signal[string]
	// marks are the plugins' icons, parsed.
	marks map[*plugin.Plugin]*core.SVG
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
	// stack is the pages opened from the toolbar's page, the first, the
	// last showing; opened counts the pages opened.
	stack  *core.Signal[[]route]
	opened int
	// searches counts the toolbar's search being pressed, which focuses
	// the search page's field.
	searches *core.Signal[int]
	// news counts the shows with new episodes of each plugin following.
	news *core.Signal[map[*plugin.Plugin]int]
	// play plays a part of a video from start, which 0 leaves to the
	// resume offer unless fromStart.
	play func(p *plugin.Plugin, v *plugin.Video, part int, start time.Duration, fromStart bool)
}

type routeKind int

const (
	routeHome routeKind = iota
	routeFollowing
	routeDiscover
	routeOpen
	routeSearch
	routeSettings
	routeBrowse
	routeVideo
	routePlugin
)

// statsPages name the kinds of page in the app's statistics.
var statsPages = map[routeKind]string{routeOpen: "open", routeHome: "home", routeDiscover: "discover", routeBrowse: "browse",
	routeSearch: "search", routeVideo: "video", routeSettings: "settings", routePlugin: "plugin", routeFollowing: "following"}

// route is a page: a toolbar's page of every plugin, or one of a
// plugin's opened from it.
type route struct {
	// n tells the pages opened apart.
	n      int
	kind   routeKind
	plugin *plugin.Plugin
	// refused is the plugin a plugin page shows when the app refused it.
	refused *refusal
	// id is browse's or video's, and title what the page's bar shows.
	id, title string
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

// choose starts over at a toolbar's page, or goes back to it when it
// shows under the pages opened from it.
func (sh *shell) choose(r route) {
	if s := sh.stack.GetUntracked(); s[0].kind == r.kind {
		if len(s) > 1 {
			sh.stack.Set(s[:1])
		}
		return
	}
	sh.opened++
	r.n = sh.opened
	sh.stack.Set([]route{r})
}

// startSearch shows the search page, its field focused.
func (sh *shell) startSearch() {
	sh.choose(route{kind: routeSearch})
	sh.searches.Update(func(n int) int { return n + 1 })
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

// content shows the page on top of the stack, keeping those under it,
// under the bar of a page opened over a toolbar's.
func (sh *shell) content(cx *core.Scope, pl *player) core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1},
		Children: []core.Node{
			sh.subpageBar(cx),
			core.View{TestID: "content", Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: []core.Node{
				core.Each(sh.stack.Get, func(r route) int { return r.n }, func(cx *core.Scope, item func() route, index func() int) core.Node {
					return core.View{
						StyleFunc: func() core.Style {
							if index() == len(sh.stack.Get())-1 {
								return core.Style{FlexGrow: 1, FlexShrink: 1}
							}
							return core.Style{Display: core.DisplayNone}
						},
						Children: []core.Node{sh.page(cx, pl, item())},
					}
				}),
			}},
		},
	}
}

// page is what a route shows.
func (sh *shell) page(cx *core.Scope, pl *player, r route) core.Node {
	switch r.kind {
	case routeHome:
		return sh.homePage(cx)
	case routeFollowing:
		return sh.followingPage(cx)
	case routeDiscover:
		return sh.discoverPage(cx)
	case routeBrowse:
		return sh.browsePage(cx, r.plugin, r.id)
	case routeSearch:
		return sh.searchPage(cx)
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
	// close closes the player, as its close button does.
	close func()
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
		pl.remember(p, v)
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

// remember puts v first among the videos played, as its item, for the
// home page to offer to continue.
func (pl *player) remember(p *plugin.Plugin, v *plugin.Video) {
	if pl.sh.recent == nil {
		return
	}
	it := plugin.Item{Kind: plugin.KindVideo, ID: v.ID, Title: v.Title, Image: v.Image, Duration: v.Duration}
	if v.Uploader != nil {
		it.Subtitle = v.Uploader.Title
	}
	if it.Duration == 0 && len(v.Parts) == 1 {
		it.Duration = v.Parts[0].Duration
	}
	data, err := json.Marshal(it)
	if err != nil {
		return
	}
	recent, id := pl.sh.recent, p.Manifest.ID
	go func() {
		if err := recent.Add(id, v.ID, data); err != nil {
			log.Printf("recent videos: %v", err)
		}
	}()
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
		src.Badge = pl.sh.badge(pt.p, badgeSmall)
		if pt.p.Has(plugin.ExportDanmaku) {
			src.Danmaku = danmakuOf(pt.p, pt.video.ID, pt.video.Parts[pt.index].ID)
		}
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

// danmakuOf loads the comments of p's part of video for the player.
func danmakuOf(p *plugin.Plugin, video, part string) playback.DanmakuLoader {
	return func(ctx context.Context, from, to time.Duration) ([]playback.Comment, error) {
		list, err := p.Danmaku(ctx, plugin.DanmakuParams{Video: video, Part: part, From: from.Seconds(), To: to.Seconds()})
		if err != nil {
			return nil, err
		}
		out := make([]playback.Comment, 0, len(list))
		for _, d := range list {
			c := playback.Comment{At: seconds(d.Time), Text: d.Text}
			switch d.Mode {
			case plugin.DanmakuTop:
				c.Mode = playback.Top
			case plugin.DanmakuBottom:
				c.Mode = playback.Bottom
			}
			if v, err := strconv.ParseUint(strings.TrimPrefix(d.Color, "#"), 16, 32); err == nil && d.Color != "" && v != 0xffffff {
				c.Color, c.Colored = core.RGB(uint32(v)), true
			}
			out = append(out, c)
		}
		return out, nil
	}
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

// page plays what plays: in a player window of its own where the
// platform opens windows, else over the main window.
func (pl *player) page(cx *core.Scope) core.Node {
	var win *desktop.Window
	closed := func() {
		win = nil
		pl.now = nil
		pl.playing.Set(nil)
		pl.sh.played.Update(func(n int) int { return n + 1 })
	}
	pl.close = func() {
		w := win
		closed()
		if w != nil {
			w.Close()
		}
	}
	// The last source stays while the page closes.
	var last playback.Source
	source := func() playback.Source {
		if s := pl.playing.Get(); s != nil {
			last = *s
		}
		return last
	}
	page := func(window bool) playback.Page {
		return playback.Page{
			Source:     source,
			Positions:  pl.positions,
			Settings:   pl.sh.settings,
			Client:     pl.client,
			Window:     window,
			OnClose:    pl.close,
			OnQuality:  pl.switchQuality,
			OnPart:     pl.playIndex,
			OnEnded:    pl.next,
			OnPosition: pl.report,
		}
	}
	// over is set where no window opens, and the page covers the main
	// window instead.
	over := core.NewSignal(cx, false)
	cx.Effect(func() {
		s := pl.playing.Get()
		cx.Untrack(func() {
			if s == nil || over.GetUntracked() {
				return
			}
			if win != nil && !win.Closed() {
				win.Focus()
				return
			}
			w := desktop.OpenWindow(cx, desktop.WindowOptions{
				Key:         "player",
				Title:       s.Title,
				Width:       playback.WindowWidth,
				Height:      playback.WindowHeight,
				Transparent: true,
				Constraints: desktop.WindowConstraints{MinWidth: 320, MinHeight: 180},
			}, func(cx *core.Scope) core.Node {
				return core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{page(true)}}
			})
			if w == nil {
				over.Set(true)
				return
			}
			win = w
			w.OnClosing(cx, func(*desktop.WindowClosing) {
				if win == w {
					closed()
				}
			})
		})
	})
	cx.Cleanup(func() {
		if win != nil {
			win.Close()
		}
	})
	return core.Show{
		When: func() bool { return over.Get() && pl.playing.Get() != nil },
		Then: func(cx *core.Scope) core.Node { return page(false) },
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
