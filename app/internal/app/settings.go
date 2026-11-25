package app

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"slices"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/i18n"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/locales"
)

// languages are the app's languages, each by its own name; "" follows the
// system.
var languages = []struct{ tag, name string }{
	{"en", "English"},
	{"zh-Hans", "简体中文"},
}

// settingsPage chooses the app's language and lists the plugins: each
// one's switch and page, and adding more.
func (sh *shell) settingsPage(cx *core.Scope) core.Node {
	return page("settings",
		group(locales.General(cx),
			row("language-row", locales.Language(cx), "", false, sh.languageSelect(cx)),
			row("stats-row", locales.SendStats(cx), locales.SendStatsDetail(cx), true, sh.statsSwitch(cx)),
		),
		core.View{Class: "gap-2", Children: []core.Node{
			core.View{Class: "flex-row items-center gap-2 px-1", Children: []core.Node{
				core.Text{Content: locales.Plugins(cx), Class: "flex-1 text-xs font-semibold text-muted-foreground"},
				sh.checkStatus(cx),
				sh.addMenu(cx),
			}},
			sh.checkFailed(cx),
			core.View{TestID: "plugin-list", Class: "overflow-hidden rounded-xl border border-border bg-background", Children: []core.Node{
				core.Each(sh.plugins.Get, func(p *plugin.Plugin) *plugin.Plugin { return p }, func(cx *core.Scope, item func() *plugin.Plugin, index func() int) core.Node {
					return sh.pluginRow(cx, item(), func() bool { return index() == len(sh.plugins.Get())-1 && len(sh.refused.Get()) == 0 })
				}),
				core.Each(sh.refused.Get, func(r *refusal) *refusal { return r }, func(cx *core.Scope, item func() *refusal, index func() int) core.Node {
					return sh.refusedRow(cx, item(), func() bool { return index() == len(sh.refused.Get())-1 })
				}),
				core.Show{When: sh.onlyBuiltIn, Then: sh.noPluginsAdded},
			}},
		}},
	)
}

// page is a settings page's scrolling column.
func page(testID string, children ...core.Node) core.Node {
	return ui.ScrollView{
		TestID:       testID,
		Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
		ContentStyle: core.Style{Padding: core.Symmetric(24, 28), AlignItems: core.AlignCenter},
		Children: []core.Node{core.View{
			Style:    core.Style{Width: core.Pct(100), MaxWidth: core.Pt(680), Gap: 24},
			Children: children,
		}},
	}
}

// onlyBuiltIn reports whether the viewer added no plugin yet.
func (sh *shell) onlyBuiltIn() bool {
	if len(sh.refused.Get()) > 0 {
		return false
	}
	for _, p := range sh.plugins.Get() {
		if !p.Builtin {
			return false
		}
	}
	return true
}

// noPluginsAdded ends the list of plugins while only the built-in ones
// are there, offering to install one from GitHub.
func (sh *shell) noPluginsAdded(cx *core.Scope) core.Node {
	return core.View{TestID: "no-plugins", Class: "items-center gap-3 border-t border-border px-6 py-8", Children: []core.Node{
		core.View{Class: "size-12 items-center justify-center rounded-2xl bg-chip", Children: []core.Node{
			core.Icon{SVG: lucide.Puzzle(), Class: "size-6 text-faint-ink"},
		}},
		core.View{Class: "items-center gap-1", Children: []core.Node{
			core.Text{Content: locales.NoPluginsAdded(cx), Class: "text-[13px] font-semibold text-foreground"},
			core.Text{Content: locales.PluginsOnGitHub(cx), Class: "max-w-[400px] text-center text-xs leading-relaxed text-muted-foreground"},
		}},
		ui.Button{TestID: "empty-install-github", Text: locales.InstallFromGitHubAction(cx), Icon: lucide.GitFork, OnPress: sh.openFromGitHub},
	}}
}

// addMenu adds a plugin: from a folder or a zip, copied into the app, or
// from a path, used where it is.
func (sh *shell) addMenu(cx *core.Scope) core.Node {
	return ui.Menu{
		TestID:  "add-plugin",
		Label:   locales.AddPlugin(cx),
		Trigger: ui.Button{Text: locales.AddPlugin(cx), Icon: lucide.Plus, Variant: ui.ButtonOutline, Size: ui.SizeSmall},
		Children: []core.Node{
			// GitHub comes first, as plugins are published there.
			ui.MenuItem{TestID: "install-github", Text: locales.FromGitHub(cx), Icon: lucide.GitFork, OnSelect: sh.openFromGitHub},
			ui.MenuSeparator{},
			ui.MenuItem{TestID: "install-folder", Text: locales.InstallFromFolder(cx), Icon: lucide.Folder, OnSelect: func() { sh.pickFolder(cx) }},
			ui.MenuItem{TestID: "install-zip", Text: locales.InstallFromZip(cx), Icon: lucide.FileArchive, OnSelect: func() { sh.pickZip(cx) }},
			ui.MenuItem{TestID: "link-folder", Text: locales.UseFromPath(cx), Icon: lucide.FolderCode, OnSelect: func() { sh.pickLink(cx) }},
		},
	}
}

func (sh *shell) languageSelect(cx *core.Scope) core.Node {
	lang := core.NewSignal(cx, sh.settings.Language())
	options := []ui.Option[string]{{Value: "", Label: locales.SystemLanguage(cx)}}
	for _, l := range languages {
		options = append(options, ui.Option[string]{Value: l.tag, Label: l.name})
	}
	return ui.Select[string]{
		TestID:  "language",
		Label:   locales.Language(cx),
		Options: options,
		Value:   lang.Get,
		OnChange: func(v string) {
			lang.Set(v)
			if err := sh.settings.SetLanguage(v); err != nil {
				log.Printf("settings: %v", err)
			}
			// The plugins take the language before the pages that
			// translate their texts follow it.
			cx.Batch(func() {
				cx.SetLocale(v)
				shown := core.Untracked(cx, func() string { return i18n.Language(cx) })
				for _, p := range sh.plugins.GetUntracked() {
					p.SetLanguage(shown)
				}
			})
		},
		Style: core.Style{Width: core.Pt(160)},
	}
}

// statsSwitch turns the usage statistics on or off.
func (sh *shell) statsSwitch(cx *core.Scope) core.Node {
	on := core.NewSignal(cx, sh.settings.Stats())
	return ui.Switch{TestID: "stats", Label: locales.SendStats(cx), Checked: on.Get, OnChange: func(v bool) {
		on.Set(v)
		if err := sh.settings.SetStats(v); err != nil {
			log.Printf("settings: %v", err)
		}
	}}
}

// source is where a plugin comes from, as its row and page say: built
// into the app, the folder it is used from, or the hosts an installed one
// reaches.
func (sh *shell) source(cx *core.Scope, p *plugin.Plugin) (func() *core.SVG, string) {
	if p.Builtin {
		return lucide.Package, locales.BuiltIntoApp(cx)
	}
	if dir, ok := sh.library.linkedDir(p); ok {
		return lucide.Folder, dir
	}
	if r, ok := sh.library.releaseOf(p); ok {
		return lucide.GitFork, r.Repo
	}
	if hosts := p.Manifest.Permissions.Network; len(hosts) > 0 {
		return lucide.Globe, strings.Join(hosts, ", ")
	}
	return lucide.WifiOff, locales.NoNetwork(cx)
}

// pluginRow is a plugin of the settings' list: its tile, name, version
// and where it comes from, with its switch; pressing it opens its page.
func (sh *shell) pluginRow(cx *core.Scope, p *plugin.Plugin, last func() bool) core.Node {
	id := p.Manifest.ID
	icon, from := sh.source(cx, p)
	version := p.Manifest.Version
	if p.Builtin {
		version = locales.BuiltIn(cx)
	}
	off := func() bool { return !sh.enabled(p) }
	return core.View{
		TestID: "plugin-row-" + id, Role: core.RoleButton, Label: p.Name(),
		OnPress: func(core.PressEvent) { sh.openPlugin(p) },
		ClassFunc: func() string {
			c := "group min-h-[60px] flex-row items-center gap-3 px-4 py-2.5 hover:bg-surface-hover"
			if !last() {
				c += " border-b border-border"
			}
			return c
		},
		Children: []core.Node{
			core.View{ClassFunc: func() string {
				if off() {
					return "opacity-50"
				}
				return ""
			}, Children: []core.Node{tile(p.Name(), "size-9 rounded-xl", "text-[15px]", colorsOf(id))}},
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
					core.Text{Content: p.Name(), MaxLines: 1, ClassFunc: func() string {
						if off() {
							return "text-[13px] font-semibold text-muted-foreground"
						}
						return "text-[13px] font-semibold text-foreground"
					}},
					core.Text{Content: version, Class: "text-xs text-faint-ink"},
				}},
				core.View{Class: "flex-row items-center gap-1", Children: []core.Node{
					core.Icon{SVG: icon(), Class: "size-3 text-faint-ink"},
					core.Text{TestID: "plugin-source-" + id, Content: from, MaxLines: 1, Class: "text-xs text-muted-foreground", Style: core.Style{FlexShrink: 1}},
				}},
			}},
			sh.updatePill(cx, p),
			sh.enableSwitch(cx, p, "enable-"),
			core.Icon{SVG: lucide.ChevronRight(), Class: "size-4 text-faint-ink"},
		},
	}
}

// enableSwitch enables or disables p; its test ID is prefix and p's id.
func (sh *shell) enableSwitch(cx *core.Scope, p *plugin.Plugin, prefix string) core.Node {
	return ui.Switch{
		TestID:   prefix + p.Manifest.ID,
		Label:    p.Name(),
		Checked:  func() bool { return sh.enabled(p) },
		OnChange: func(on bool) { sh.setEnabled(p, on) },
	}
}

// refusedRow is a plugin the app refused, with why.
func (sh *shell) refusedRow(cx *core.Scope, r *refusal, last func() bool) core.Node {
	name := r.title()
	return core.View{
		TestID: "refused-row-" + r.key(), Role: core.RoleButton, Label: name,
		OnPress: func(core.PressEvent) { sh.open(route{kind: routePlugin, refused: r, title: name}) },
		ClassFunc: func() string {
			c := "min-h-[60px] flex-row items-center gap-3 px-4 py-2.5 hover:bg-surface-hover"
			if !last() {
				c += " border-b border-border"
			}
			return c
		},
		Children: []core.Node{
			tile(name, "size-9 rounded-xl", "text-[15px]", greyTile),
			core.View{Class: "flex-1 gap-0.5", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.View{Class: "flex-row items-center gap-2", Children: []core.Node{
					core.Text{Content: name, MaxLines: 1, Class: "text-[13px] font-semibold text-foreground"},
					core.Text{Content: r.version, Class: "text-xs text-faint-ink"},
				}},
				core.View{Class: "flex-row items-center gap-1", Children: []core.Node{
					core.Icon{SVG: lucide.Folder(), Class: "size-3 text-faint-ink"},
					core.Text{Content: r.where(sh), MaxLines: 1, Class: "text-xs text-muted-foreground", Style: core.Style{FlexShrink: 1}},
				}},
			}},
			core.View{Class: "h-6 flex-row items-center gap-1 rounded-full bg-warn-soft px-2.5", Children: []core.Node{
				core.Icon{SVG: lucide.TriangleAlert(), Class: "size-3.5 text-warn-icon"},
				core.Text{Content: refusedLabel(cx, r.err), Class: "text-xs font-semibold text-warn-ink"},
			}},
			core.Icon{SVG: lucide.ChevronRight(), Class: "size-4 text-faint-ink"},
		},
	}
}

// title is a refused plugin's name, else its id or folder.
func (r *refusal) title() string {
	for _, s := range []string{r.name, r.id, r.folder, r.dir} {
		if s != "" {
			return s
		}
	}
	return "?"
}

// key tells refused plugins apart.
func (r *refusal) key() string {
	if r.dir != "" {
		return r.dir
	}
	return r.folder
}

// where is where a refused plugin is.
func (r *refusal) where(sh *shell) string {
	if r.dir != "" {
		return r.dir
	}
	return r.folder
}

// refusedLabel is why the app refused a plugin, in a few words.
func refusedLabel(cx *core.Scope, err error) string {
	var tooNew *plugin.TooNewError
	if errors.As(err, &tooNew) {
		return locales.NeedsApp(cx, tooNew.MinAppVersion)
	}
	return locales.CannotLoad(cx)
}

// setEnabled enables or disables p; a disabled plugin's pages close and
// its runtime stops.
func (sh *shell) setEnabled(p *plugin.Plugin, on bool) {
	if err := sh.settings.SetEnabled(p.Manifest.ID, on); err != nil {
		log.Printf("settings: %v", err)
	}
	sh.toggled.Update(func(n int) int { return n + 1 })
	if !on {
		sh.closePages(p, false)
		go p.Stop()
	}
}

// closePages closes the pages of p, but for its own page in the
// settings unless all.
func (sh *shell) closePages(p *plugin.Plugin, all bool) {
	sh.stack.Update(func(s []route) []route {
		kept := slices.DeleteFunc(slices.Clone(s), func(r route) bool {
			return r.plugin == p && (all || r.kind != routePlugin)
		})
		if len(kept) == 0 {
			sh.opened++
			kept = []route{{n: sh.opened, kind: routeHome}}
		}
		return kept
	})
}

// openPlugin opens p's page, from the settings.
func (sh *shell) openPlugin(p *plugin.Plugin) {
	if top := sh.top(); top.kind == routePlugin && top.plugin == p {
		return
	}
	sh.open(route{kind: routePlugin, plugin: p, title: p.Name()})
}

func (sh *shell) pickFolder(cx *core.Scope) {
	cx.PickDirectories(core.PickDirectoriesOptions{}, func(dirs []core.Directory, err error) {
		if err != nil {
			sh.installFailed(cx, err)
			return
		}
		if len(dirs) > 0 {
			sh.inspect(cx, func() (fs.FS, error) { return dirs[0], nil }, dirs[0].Path)
		}
	})
}

func (sh *shell) pickZip(cx *core.Scope) {
	cx.PickFiles(core.PickFilesOptions{Types: []string{".zip"}}, func(files []core.File, err error) {
		if err != nil {
			sh.installFailed(cx, err)
			return
		}
		if len(files) > 0 && files[0].Path != "" {
			path := files[0].Path
			sh.inspect(cx, func() (fs.FS, error) { return readZip(path) }, path)
		}
	})
}

// pickLink uses the plugin in a folder the viewer picks where it is,
// without installing it.
func (sh *shell) pickLink(cx *core.Scope) {
	cx.PickDirectories(core.PickDirectoriesOptions{}, func(dirs []core.Directory, err error) {
		if err != nil {
			sh.installFailed(cx, err)
			return
		}
		if len(dirs) > 0 && dirs[0].Path != "" {
			sh.link(cx, dirs[0].Path)
		}
	})
}

// link reads the plugin in the folder of path and asks the viewer to
// approve its permissions before using it there.
func (sh *shell) link(cx *core.Scope, path string) {
	sh.ask(cx, func(taken func(string) bool) (*candidate, error) { return sh.library.inspectDir(path, taken) })
}

// inspect reads the plugin open answers, from where, and asks the viewer
// to approve its permissions.
func (sh *shell) inspect(cx *core.Scope, open func() (fs.FS, error), where string) {
	sh.ask(cx, func(taken func(string) bool) (*candidate, error) {
		src, err := open()
		if err != nil {
			return nil, err
		}
		c, err := sh.library.inspect(src, taken)
		if c != nil {
			c.from = where
		}
		return c, err
	})
}

// ask reads a candidate off the engine goroutine, given the ids taken,
// and asks the viewer to approve its permissions.
func (sh *shell) ask(cx *core.Scope, read func(taken func(string) bool) (*candidate, error)) {
	taken := map[string]bool{}
	for _, p := range sh.plugins.GetUntracked() {
		taken[p.Manifest.ID] = true
	}
	call(cx, sh.ctx, func(context.Context) (*candidate, error) {
		return read(func(id string) bool { return taken[id] })
	}, func(c *candidate, err error) {
		if err != nil {
			sh.installFailed(cx, err)
			return
		}
		sh.install.Set(installState{})
		sh.installing.Set(c)
	})
}

// installFailed says why a plugin picked cannot be installed.
func (sh *shell) installFailed(cx *core.Scope, err error) {
	sh.toaster.Show(ui.ToastContent{ID: "install", Type: ui.ToastError, Title: locales.CannotInstall(cx), Description: installError(cx, err)})
}

// installError is why installing a plugin failed.
func installError(cx *core.Scope, err error) string {
	var tooNew *plugin.TooNewError
	if errors.As(err, &tooNew) {
		return locales.NeedsNewerApp(cx, tooNew.MinAppVersion)
	}
	if m, ok := gitHubMessage(cx, err); ok {
		return m
	}
	return err.Error()
}

// pluginPage is a plugin's own page: its switch, account, settings, the
// hosts it reaches, and removing it.
func (sh *shell) pluginPage(cx *core.Scope, p *plugin.Plugin) core.Node {
	id := p.Manifest.ID
	icon, from := sh.source(cx, p)
	line := []core.Node{}
	if p.Builtin {
		line = append(line, core.Text{Content: locales.BuiltIntoApp(cx), Class: "text-[13px] text-muted-foreground"})
	} else {
		source := core.Node(core.Text{Content: from, MaxLines: 1, Class: "text-[13px] font-medium text-muted-foreground", Style: core.Style{FlexShrink: 1}})
		if r, ok := sh.library.releaseOf(p); ok {
			// The repository opens in the browser.
			source = core.Text{TestID: "repository-link", Content: r.Repo, MaxLines: 1, Role: core.RoleLink, Label: r.Repo,
				OnPress: func(core.PressEvent) { cx.OpenURL("https://github.com/"+r.Repo, nil) },
				Class:   "text-[13px] font-medium text-accent", Style: core.Style{FlexShrink: 1, Cursor: core.CursorPointer}}
		}
		line = append(line,
			core.Text{Content: locales.VersionN(cx, p.Manifest.Version), Class: "text-[13px] text-muted-foreground"},
			core.Text{Content: "·", Class: "text-[13px] text-faint-ink"},
			core.Icon{SVG: icon(), Class: "size-3.5 text-faint-ink"},
			source,
		)
	}
	children := []core.Node{
		core.View{Class: "flex-row items-center gap-4", Children: []core.Node{
			tile(p.Name(), "size-14 rounded-2xl shadow-md", "text-2xl", colorsOf(id)),
			core.View{Class: "flex-1 gap-1", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.Text{Content: p.Name(), Class: "text-xl font-bold text-foreground"},
				core.View{Class: "flex-row items-center gap-1.5", Children: line},
			}},
			sh.enableSwitch(cx, p, "page-enable-"),
		}},
		sh.updateBanner(cx, p),
	}
	if p.Has(plugin.ExportAccount) || p.Has(plugin.ExportSignIn) {
		children = append(children, group(locales.Account(cx), sh.accountRow(cx, p)))
	}
	if len(p.Manifest.Settings) > 0 {
		var rows []core.Node
		for i, s := range p.Manifest.Settings {
			rows = append(rows, sh.settingRow(cx, p, s, i == len(p.Manifest.Settings)-1))
		}
		children = append(children, core.View{TestID: "settings-" + id, Children: []core.Node{group(locales.Settings(cx), rows...)}})
	}
	hosts := p.Manifest.Permissions.Network
	var network core.Node
	if len(hosts) == 0 {
		network = core.View{Class: "flex-row items-center gap-2 rounded-xl border border-border bg-background px-4 py-3", Children: []core.Node{
			core.Icon{SVG: lucide.WifiOff(), Class: "size-4 text-faint-ink"},
			core.Text{Content: locales.ReachesNoHosts(cx), Class: "text-[13px] text-muted-foreground"},
		}}
	} else {
		var chips []core.Node
		for _, h := range hosts {
			chips = append(chips, hostChip(h))
		}
		network = core.View{Class: "gap-2 rounded-xl border border-border bg-background px-4 py-3", Children: []core.Node{
			core.Text{Content: locales.ReachesOnly(cx), Class: "text-xs text-muted-foreground"},
			core.View{Class: "flex-row flex-wrap gap-1.5", Children: chips},
		}}
	}
	children = append(children, core.View{TestID: "network-" + id, Class: "gap-2", Children: []core.Node{
		core.Text{Content: locales.Network(cx), Class: "px-1 text-xs font-semibold text-muted-foreground"},
		network,
	}})
	if !p.Builtin {
		children = append(children, removeButton(cx, "remove-"+id, func() {
			_, linked := sh.library.linkedDir(p)
			sh.removing.Set(&removal{plugin: p, name: p.Name(), linked: linked})
		}))
	}
	return page("plugin-page", children...)
}

// removeButton asks to remove a plugin.
func removeButton(cx *core.Scope, testID string, ask func()) core.Node {
	return core.View{Class: "flex-row items-center pt-1", Children: []core.Node{
		ui.Button{TestID: testID, Text: locales.RemovePluginAction(cx), Variant: ui.ButtonOutline, TextStyle: core.Style{Color: ui.ColorsOf(cx).Danger()}, OnPress: ask},
	}}
}

// refusedPage is a plugin the app refused to load: what is wrong, and
// removing it.
func (sh *shell) refusedPage(cx *core.Scope, r *refusal) core.Node {
	name := r.title()
	line := []core.Node{}
	if r.version != "" {
		line = append(line,
			core.Text{Content: locales.VersionN(cx, r.version), Class: "text-[13px] text-muted-foreground"},
			core.Text{Content: "·", Class: "text-[13px] text-faint-ink"},
		)
	}
	line = append(line,
		core.Icon{SVG: lucide.Folder(), Class: "size-3.5 text-faint-ink"},
		core.Text{Content: r.where(sh), MaxLines: 1, Class: "text-[13px] font-medium text-muted-foreground", Style: core.Style{FlexShrink: 1}},
	)
	why := r.err.Error()
	var tooNew *plugin.TooNewError
	if errors.As(r.err, &tooNew) {
		why = locales.NeedsNewerAppToUse(cx, tooNew.MinAppVersion)
	}
	return page("plugin-page",
		core.View{Class: "flex-row items-center gap-4", Children: []core.Node{
			tile(name, "size-14 rounded-2xl", "text-2xl", greyTile),
			core.View{Class: "flex-1 gap-1", Style: core.Style{FlexShrink: 1}, Children: []core.Node{
				core.Text{Content: name, Class: "text-xl font-bold text-foreground"},
				core.View{Class: "flex-row items-center gap-1.5", Children: line},
			}},
		}},
		banner("refused", true, lucide.TriangleAlert, locales.CannotBeLoaded(cx), why, nil),
		removeButton(cx, "remove-refused", func() { sh.removing.Set(&removal{refused: r, name: name, linked: r.dir != ""}) }),
	)
}

// settingRow is a setting p declares, saved as the viewer changes it.
func (sh *shell) settingRow(cx *core.Scope, p *plugin.Plugin, s plugin.Setting, last bool) core.Node {
	v, err := p.Setting(s.Key)
	if err != nil {
		v = nil
	}
	value := core.NewSignalFunc[any](cx, v, func(a, b any) bool { return a == b })
	ctx := cx.Context()
	save := func(v any) {
		if v == value.GetUntracked() {
			return
		}
		value.Set(v)
		call(cx, ctx, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.SetSetting(ctx, s.Key, v) }, func(_ struct{}, err error) {
			if err != nil {
				sh.fail(cx, p, err)
			}
		})
	}
	id := "setting-" + p.Manifest.ID + "-" + s.Key
	label := p.Translate(s.Label)
	var control core.Node
	switch s.Type {
	case plugin.SettingToggle:
		control = ui.Switch{TestID: id, Label: label, Checked: func() bool { b, _ := value.Get().(bool); return b }, OnChange: func(on bool) { save(on) }}
	case plugin.SettingChoice:
		var options []ui.Option[string]
		for _, c := range s.Choices {
			options = append(options, ui.Option[string]{Value: c.Value, Label: p.Translate(c.Label)})
		}
		control = ui.Select[string]{TestID: id, Label: label, Options: options, Value: func() string { t, _ := value.Get().(string); return t }, OnChange: func(t string) { save(t) },
			Style: core.Style{Width: core.Pt(160)}}
	default:
		text := core.NewSignal(cx, "")
		if t, ok := v.(string); ok {
			text.Set(t)
		}
		control = ui.Input{
			TestID:   id,
			Label:    label,
			Masked:   s.Type == plugin.SettingSecret,
			Value:    text.Get,
			OnChange: text.Set,
			OnSubmit: func(t string) { save(t) },
			OnBlur:   func() { save(text.GetUntracked()) },
			Style:    core.Style{Width: core.Pt(240)},
		}
	}
	return row("", label, "", last, control)
}

// removal is a plugin the viewer is asked to remove: one loaded, or one
// the app refused.
type removal struct {
	plugin  *plugin.Plugin
	refused *refusal
	name    string
	// linked is one used where its folder is, which stays.
	linked bool
}

// removeDialog asks the viewer to confirm removing a plugin with its
// data.
func (sh *shell) removeDialog(cx *core.Scope) core.Node {
	remove := func() {
		rm := sh.removing.Get()
		sh.removing.Set(nil)
		if rm == nil {
			return
		}
		done := func(_ struct{}, err error) {
			if err != nil {
				sh.toaster.Show(ui.ToastContent{ID: "remove", Type: ui.ToastError, Title: locales.CannotRemove(cx), Description: err.Error()})
			}
		}
		if r := rm.refused; r != nil {
			sh.stack.Update(func(s []route) []route {
				return slices.DeleteFunc(slices.Clone(s), func(x route) bool { return x.refused == r })
			})
			sh.refused.Update(func(rs []*refusal) []*refusal {
				return slices.DeleteFunc(slices.Clone(rs), func(x *refusal) bool { return x == r })
			})
			keep := slices.ContainsFunc(sh.plugins.GetUntracked(), func(p *plugin.Plugin) bool { return p.Manifest.ID == r.id })
			call(cx, sh.ctx, func(context.Context) (struct{}, error) { return struct{}{}, sh.library.removeRefused(r, keep) }, done)
			return
		}
		p := rm.plugin
		sh.closePages(p, true)
		sh.plugins.Update(func(ps []*plugin.Plugin) []*plugin.Plugin {
			return slices.DeleteFunc(slices.Clone(ps), func(q *plugin.Plugin) bool { return q == p })
		})
		delete(sh.accounts, p)
		sh.updates.Update(func(m map[string]*update) map[string]*update {
			m = clonedUpdates(m)
			delete(m, p.Manifest.ID)
			return m
		})
		if err := sh.settings.SetEnabled(p.Manifest.ID, true); err != nil {
			log.Printf("settings: %v", err)
		}
		call(cx, sh.ctx, func(context.Context) (struct{}, error) { return struct{}{}, sh.library.remove(p) }, done)
	}
	return ui.Dialog{
		TestID: "remove",
		Role:   core.RoleAlertDialog,
		Bare:   true,
		Open:   func() bool { return sh.removing.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				sh.removing.Set(nil)
			}
		},
		LabelFunc: func() string {
			if rm := sh.removing.Get(); rm != nil {
				return locales.RemovePlugin(cx, rm.name)
			}
			return ""
		},
		Style: core.Style{Width: core.Pt(420), CornerRadius: 16},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			rm := sh.removing.Get()
			if rm == nil {
				return nil
			}
			detail := locales.RemovesData(cx)
			if rm.linked {
				detail = locales.FolderStays(cx)
			}
			return core.View{Children: []core.Node{
				core.View{Class: "gap-3 px-6 pt-6 pb-5", Children: []core.Node{
					core.View{Class: "size-11 items-center justify-center rounded-2xl bg-error-soft", Children: []core.Node{
						core.Icon{SVG: lucide.Trash(), Class: "size-5 text-error-icon"},
					}},
					core.Text{Content: locales.RemovePlugin(cx, rm.name), Class: "text-[17px] font-bold text-foreground"},
					core.Text{Content: detail, Class: "text-[13px] leading-relaxed text-muted-foreground"},
				}},
				dialogActions(nil,
					ui.Button{TestID: "cancel-remove", Text: locales.Cancel(cx), Variant: ui.ButtonOutline, OnPress: func() { sh.removing.Set(nil) }},
					ui.Button{TestID: "confirm-remove", Text: locales.Remove(cx), Variant: ui.ButtonDanger, OnPress: remove},
				),
			}}
		})},
	}
}

// dialogActions is a dialog's footer: lead at its start, then its
// buttons at its end.
func dialogActions(lead core.Node, buttons ...core.Node) core.Node {
	children := []core.Node{core.View{Class: "flex-1 flex-row items-center", Children: []core.Node{lead}}}
	children = append(children, buttons...)
	return core.View{Class: "flex-row items-center justify-end gap-2 border-t border-border bg-chip/60 px-5 py-3.5", Children: children}
}
