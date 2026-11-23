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

// settingsPage chooses the app's language and manages the plugins: each
// one's switch, installing, removing and the settings of the enabled ones.
func (sh *shell) settingsPage(cx *core.Scope) core.Node {
	return ui.ScrollView{
		TestID:       "settings",
		Style:        core.Style{FlexGrow: 1, FlexShrink: 1},
		ContentStyle: core.Style{Padding: core.All(16), Gap: 24, MaxWidth: core.Pt(720)},
		Children: []core.Node{
			ui.FormSection{Children: []core.Node{
				ui.FormRow{Title: locales.Language(cx), Control: sh.languageSelect(cx)},
			}},
			ui.FormSection{
				Title: locales.Plugins(cx),
				Children: []core.Node{
					core.Each(sh.plugins.Get, func(p *plugin.Plugin) *plugin.Plugin { return p }, func(cx *core.Scope, item func() *plugin.Plugin, _ func() int) core.Node {
						return sh.pluginRow(cx, item())
					}),
					ui.FormRow{TestID: "install-folder", Title: locales.InstallFromFolder(cx), Variant: ui.FormRowAccent, OnPress: func() { sh.pickFolder(cx) }},
					ui.FormRow{TestID: "install-zip", Title: locales.InstallFromZip(cx), Variant: ui.FormRowAccent, OnPress: func() { sh.pickZip(cx) }},
					sh.linkRow(cx),
				},
			},
			core.Each(sh.active, func(p *plugin.Plugin) *plugin.Plugin { return p }, func(cx *core.Scope, item func() *plugin.Plugin, _ func() int) core.Node {
				return sh.pluginSettings(cx, item())
			}),
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
	}
}

// pluginRow is a plugin's name, version and the hosts it reaches, with
// its switch, and for an installed one, removing it.
func (sh *shell) pluginRow(cx *core.Scope, p *plugin.Plugin) core.Node {
	id := p.Manifest.ID
	detail := p.Manifest.Version
	if hosts := p.Manifest.Permissions.Network; len(hosts) > 0 {
		detail += " · " + strings.Join(hosts, ", ")
	}
	if dir, ok := sh.library.linkedDir(p); ok {
		detail += " · " + dir
	}
	controls := []core.Node{}
	if !p.Builtin {
		controls = append(controls, ui.IconButton{
			TestID: "remove-" + id, Icon: lucide.Trash, Label: locales.Remove(cx), Variant: ui.IconButtonGhost,
			OnPress: func() { sh.removing.Set(p) },
		})
	}
	controls = append(controls, ui.Switch{
		TestID:   "enable-" + id,
		Label:    p.Name(),
		Checked:  func() bool { return sh.enabled(p) },
		OnChange: func(on bool) { sh.setEnabled(p, on) },
	})
	return ui.FormRow{
		TestID:  "plugin-row-" + id,
		Title:   p.Name(),
		Detail:  detail,
		Control: core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12}, Children: controls},
	}
}

// setEnabled enables or disables p; a disabled plugin's pages close and
// its runtime stops.
func (sh *shell) setEnabled(p *plugin.Plugin, on bool) {
	if err := sh.settings.SetEnabled(p.Manifest.ID, on); err != nil {
		log.Printf("settings: %v", err)
	}
	sh.toggled.Update(func(n int) int { return n + 1 })
	if !on {
		sh.closePages(p)
		go p.Stop()
	}
}

// closePages closes the pages of p.
func (sh *shell) closePages(p *plugin.Plugin) {
	sh.stack.Update(func(s []route) []route {
		kept := slices.DeleteFunc(slices.Clone(s), func(r route) bool { return r.plugin == p })
		if len(kept) == 0 {
			sh.opened++
			kept = []route{{n: sh.opened, kind: routeOpen}}
		}
		return kept
	})
}

// openSettings shows the settings page from the sidebar.
func (sh *shell) openSettings() {
	if r, ok := sh.routeOf(sh.cx, "settings"); ok {
		sh.opened++
		r.n = sh.opened
		sh.stack.Set([]route{r})
	}
}

func (sh *shell) pickFolder(cx *core.Scope) {
	cx.PickDirectories(core.PickDirectoriesOptions{}, func(dirs []core.Directory, err error) {
		if err != nil {
			sh.installFailed(cx, err)
			return
		}
		if len(dirs) > 0 {
			sh.inspect(cx, func() (fs.FS, error) { return dirs[0], nil })
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
			sh.inspect(cx, func() (fs.FS, error) { return readZip(path) })
		}
	})
}

// linkRow uses the plugin in a folder the viewer types the path of where
// it is, without installing it.
func (sh *shell) linkRow(cx *core.Scope) core.Node {
	path := core.NewSignal(cx, "")
	submit := func() {
		if p := strings.TrimSpace(path.GetUntracked()); p != "" {
			sh.link(cx, p)
			path.Set("")
		}
	}
	return ui.FormRow{
		TestID: "link-folder",
		Title:  locales.UseFromPath(cx),
		Control: core.View{
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8},
			Children: []core.Node{
				ui.Input{
					TestID:      "link-path",
					Label:       locales.UseFromPath(cx),
					Placeholder: locales.PluginPath(cx),
					Value:       path.Get,
					OnChange:    path.Set,
					OnSubmit:    func(string) { submit() },
					Style:       core.Style{Width: core.Pt(240)},
				},
				ui.Button{TestID: "link", Text: locales.Use(cx), Variant: ui.ButtonSecondary, OnPress: submit},
			},
		},
	}
}

// link reads the plugin in the folder of path and asks the viewer to
// approve its permissions before using it there.
func (sh *shell) link(cx *core.Scope, path string) {
	sh.ask(cx, func(taken func(string) bool) (*candidate, error) { return sh.library.inspectDir(path, taken) })
}

// inspect reads the plugin open answers and asks the viewer to approve
// its permissions.
func (sh *shell) inspect(cx *core.Scope, open func() (fs.FS, error)) {
	sh.ask(cx, func(taken func(string) bool) (*candidate, error) {
		src, err := open()
		if err != nil {
			return nil, err
		}
		return sh.library.inspect(src, taken)
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
		sh.installing.Set(c)
	})
}

func (sh *shell) installFailed(cx *core.Scope, err error) {
	t := ui.ToastContent{ID: "install", Type: ui.ToastError, Title: locales.CannotInstall(cx), Description: err.Error()}
	var tooNew *plugin.TooNewError
	if errors.As(err, &tooNew) {
		t.Description = locales.NeedsNewerApp(cx, tooNew.MinAppVersion)
	}
	sh.toaster.Show(t)
}

// installDialog asks the viewer to approve the permissions of the plugin
// being installed.
func (sh *shell) installDialog(cx *core.Scope) core.Node {
	install := func() {
		c := sh.installing.Get()
		sh.installing.Set(nil)
		if c == nil {
			return
		}
		call(cx, sh.ctx, func(context.Context) (*plugin.Plugin, error) { return sh.library.install(c) }, func(p *plugin.Plugin, err error) {
			if err != nil {
				sh.installFailed(cx, err)
				return
			}
			if err := sh.settings.SetEnabled(p.Manifest.ID, true); err != nil {
				log.Printf("settings: %v", err)
			}
			sh.toggled.Update(func(n int) int { return n + 1 })
			sh.plugins.Update(func(ps []*plugin.Plugin) []*plugin.Plugin { return append(slices.Clip(ps), p) })
		})
	}
	return ui.AlertDialog{
		TestID: "install",
		Open:   func() bool { return sh.installing.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				sh.installing.Set(nil)
			}
		},
		TitleFunc: func() string {
			if c := sh.installing.Get(); c != nil && c.dir != "" {
				return locales.UsePlugin(cx, c.manifest.Name, c.manifest.Version)
			} else if c != nil {
				return locales.InstallPlugin(cx, c.manifest.Name, c.manifest.Version)
			}
			return ""
		},
		DescriptionFunc: func() string {
			c := sh.installing.Get()
			if c == nil {
				return ""
			}
			if hosts := c.manifest.Permissions.Network; len(hosts) > 0 {
				return locales.ReachesHosts(cx, strings.Join(hosts, "\n"))
			}
			return locales.ReachesNoHosts(cx)
		},
		Actions: []ui.AlertAction{
			{TestID: "cancel-install", Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Role: core.AlertCancel, OnPress: func() { sh.installing.Set(nil) }},
			{TestID: "confirm-install", TextFunc: func() string {
				if c := sh.installing.Get(); c != nil && c.dir != "" {
					return locales.Use(cx)
				}
				return locales.Install(cx)
			}, Default: true, OnPress: install},
		},
	}
}

// removeDialog asks the viewer to confirm removing a plugin with its
// data.
func (sh *shell) removeDialog(cx *core.Scope) core.Node {
	remove := func() {
		p := sh.removing.Get()
		sh.removing.Set(nil)
		if p == nil {
			return
		}
		sh.closePages(p)
		sh.plugins.Update(func(ps []*plugin.Plugin) []*plugin.Plugin {
			return slices.DeleteFunc(slices.Clone(ps), func(q *plugin.Plugin) bool { return q == p })
		})
		delete(sh.accounts, p)
		if err := sh.settings.SetEnabled(p.Manifest.ID, true); err != nil {
			log.Printf("settings: %v", err)
		}
		call(cx, sh.ctx, func(context.Context) (struct{}, error) { return struct{}{}, sh.library.remove(p) }, func(_ struct{}, err error) {
			if err != nil {
				sh.toaster.Show(ui.ToastContent{ID: "remove", Type: ui.ToastError, Title: locales.CannotRemove(cx), Description: err.Error()})
			}
		})
	}
	return ui.AlertDialog{
		TestID: "remove",
		Open:   func() bool { return sh.removing.Get() != nil },
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				sh.removing.Set(nil)
			}
		},
		TitleFunc: func() string {
			if p := sh.removing.Get(); p != nil {
				return locales.RemovePlugin(cx, p.Name())
			}
			return ""
		},
		DescriptionFunc: func() string {
			if p := sh.removing.Get(); p != nil {
				if _, ok := sh.library.linkedDir(p); ok {
					return locales.FolderStays(cx)
				}
			}
			return locales.RemovesData(cx)
		},
		Actions: []ui.AlertAction{
			{TestID: "cancel-remove", Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Role: core.AlertCancel, Default: true, OnPress: func() { sh.removing.Set(nil) }},
			{TestID: "confirm-remove", Text: locales.Remove(cx), Variant: ui.ButtonDanger, Role: core.AlertDestructive, OnPress: remove},
		},
	}
}

// pluginSettings are the settings p declares, each saved as the viewer
// changes it.
func (sh *shell) pluginSettings(cx *core.Scope, p *plugin.Plugin) core.Node {
	if len(p.Manifest.Settings) == 0 {
		return nil
	}
	var rows []core.Node
	for _, s := range p.Manifest.Settings {
		rows = append(rows, sh.settingRow(cx, p, s))
	}
	return ui.FormSection{TestID: "settings-" + p.Manifest.ID, Title: p.Name(), Children: rows}
}

func (sh *shell) settingRow(cx *core.Scope, p *plugin.Plugin, s plugin.Setting) core.Node {
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
		control = ui.Select[string]{TestID: id, Label: label, Options: options, Value: func() string { t, _ := value.Get().(string); return t }, OnChange: func(t string) { save(t) }}
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
	return ui.FormRow{Title: label, Control: control}
}
