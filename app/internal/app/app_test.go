package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	_ "github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/internal/store"
	"github.com/oneo-me/nsgo-media/app/plugins/local"
)

// TestMain gives the platform the main thread under nsgo test -target,
// where the blank import of nsgo shows the app in a real window.
func TestMain(m *testing.M) { nsgotest.Main(m) }

// offline refuses every request, so pictures and subtitles fail as on a
// site that is down.
type offline struct{}

func (offline) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

const wait = 5 * time.Second

// demoDir is the demo plugin's folder: by the source's path, as a test
// on a target runs elsewhere.
func demoDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "../../examples/demo-plugin")
}

func demo() fs.FS { return os.DirFS(demoDir()) }

// startDemo runs the app with the demo plugin installed and no built-in
// one, its data in dataDir, a new folder when "", and returns the player
// once it starts.
func startDemo(t *testing.T, dataDir string) (*nsgotest.App, *player) {
	t.Helper()
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	if err := store.InstallPlugin(filepath.Join(dataDir, "plugins"), "demo", demo()); err != nil && !errors.Is(err, store.ErrInstalled) {
		t.Fatal(err)
	}
	return start(t, dataDir)
}

// start runs the app on the data in dataDir, without built-in plugins.
func start(t *testing.T, dataDir string) (*nsgotest.App, *player) {
	t.Helper()
	var pl *player
	a := App{
		Builtin: []Builtin{},
		DataDir: dataDir,
		Client:  &http.Client{Transport: offline{}},
		started: func(p *player) { pl = p },
	}
	app := nsgotest.Start(t, a.Render, nsgotest.Size(1100, 700), nsgotest.Environment(core.Environment{Locale: "en-US"}))
	return app, pl
}

func TestRootShowsSidebarAndContent(t *testing.T) {
	app := nsgotest.Start(t, Root, nsgotest.Size(1100, 700))
	app.Find("id:nav")
	app.Find("id:page")

	// A built-in plugin can be disabled, not removed.
	app.Press("id:nav-settings")
	app.WaitFor("id:enable-local", wait)
	if app.Exists("id:remove-local") {
		t.Fatal("a built-in plugin can be removed")
	}
}

func TestOpeningAURLShowsThePlayer(t *testing.T) {
	app := nsgotest.Start(t, Root, nsgotest.Size(1100, 700))
	app.Type("id:url", "https://example.invalid/movie.mp4")
	app.Submit("id:url")
	app.Find("id:player >> text:movie.mp4")
	app.Press("id:back")
	app.WaitForGone("id:player", time.Second)
}

func TestPluginPages(t *testing.T) {
	app, _ := startDemo(t, "")
	app.Find("id:plugin-demo >> text:Demo")
	app.Find("id:nav-demo-popular")

	// Home: its sections, the page after it, and a section's rest.
	app.Press("id:nav-demo-home")
	app.WaitFor("id:item-video-v1", wait)
	app.Find("id:item-uploader-u1")
	app.Press("id:load-more")
	app.WaitFor("id:item-collection-c1", wait)
	app.Press("id:more-popular")
	waitText(app, "page-title", "Popular")
	app.WaitFor("id:item-video-v3", wait)
	app.Press("id:page-back")

	// An uploader's and a collection's pages.
	app.WaitFor("id:item-uploader-u2", wait)
	app.Press("id:item-uploader-u2")
	app.WaitFor("id:item-video-v2", wait)
	app.Press("id:page-back")
	app.WaitFor("id:item-collection-c1", wait)
	app.Press("id:item-collection-c1")
	waitText(app, "page-title", "Walks")
	app.WaitFor("id:item-video-v3", wait)

	// A sidebar entry of the plugin's own.
	app.Press("id:nav-demo-popular")
	app.WaitFor("id:item-video-v2", wait)
	app.WaitForGone("id:page-back", time.Second)
}

func TestSignInShowsPagesThatNeedIt(t *testing.T) {
	app, _ := startDemo(t, "")
	app.Press("id:nav-demo-favorites")
	app.WaitFor("text:Sign in to see your favorites", wait)
	app.Press("id:error-sign-in")
	app.WaitFor("id:item-video-v2", wait)
	app.WaitForGone("id:sign-in", wait)

	app.Press("id:account-demo")
	if app.Exists("id:sign-out-demo") {
		app.Press("id:sign-out-demo")
	} else {
		// On macOS, the menu is the system's.
		app.ChoosePopup("Sign Out")
	}
	app.WaitFor("text:Sign in to see your favorites", wait)
	app.Find("id:sign-in-demo")
}

func TestSignInPrompt(t *testing.T) {
	app, pl := startDemo(t, "")
	var dismissed atomic.Bool
	p := pl.sh.plugins.GetUntracked()[0]
	pl.sh.prompts.ShowSignIn(p, plugin.SignInPrompt{QRCode: "https://demo.example.com/qr", Message: "Scan it", Dismissed: func() { dismissed.Store(true) }})
	app.WaitFor("id:sign-in >> text:Sign in to Demo", wait)
	waitText(app, "sign-in-message", "Scan it")
	app.Find("id:sign-in-code")
	app.Screenshot("sign-in")
	app.Press("id:cancel-sign-in")
	app.WaitForGone("id:sign-in", time.Second)
	app.WaitUntil(time.Second, dismissed.Load)

	// A prompt the plugin closes tells it nothing.
	dismissed.Store(false)
	pl.sh.prompts.ShowSignIn(p, plugin.SignInPrompt{Message: "Scan it", Dismissed: func() { dismissed.Store(true) }})
	app.WaitFor("id:sign-in-message", wait)
	pl.sh.prompts.CloseSignIn(p)
	app.WaitForGone("id:sign-in", time.Second)
	if dismissed.Load() {
		t.Fatal("closing the prompt dismissed it")
	}
}

func TestSearch(t *testing.T) {
	app, _ := startDemo(t, "")
	app.Press("id:nav-demo-search")
	app.Type("id:query", "fog")
	app.FastForward(suggestAfter)
	app.WaitFor("id:suggestion-0 >> text:Fog in the valley", wait)
	app.Press("id:suggestion-0")
	app.WaitFor("id:item-video-v3", wait)

	app.Clear("id:query")
	app.Type("id:query", "hill")
	app.Submit("id:query")
	app.WaitFor("id:item-uploader-u1", wait)
	app.Find("id:item-video-v1")

	// A pasted link opens its video.
	app.Clear("id:query")
	app.Type("id:query", "https://demo.example.com/video/v2")
	app.Submit("id:query")
	app.WaitFor("id:video-page", wait)
	waitText(app, "page-title", "Ten-minute noodles")

	// A link to a later part plays that part.
	app.Press("id:page-back")
	app.Clear("id:query")
	app.Type("id:query", "https://demo.example.com/video/v1?part=2")
	app.Submit("id:query")
	waitText(app, "page-title", "Sunrise over the hills")
	app.WaitFor("id:play-video", wait)
	app.Press("id:play-video")
	app.WaitUntil(wait, func() bool { return app.Exists("id:player") && title(app) == "Sunrise over the hills – Summit" })
}

// waitText waits for the text of id to hold want.
func waitText(app *nsgotest.App, id, want string) {
	app.WaitUntil(wait, func() bool { return app.Exists("id:"+id) && strings.Contains(app.Find("id:"+id).Text, want) })
}

// title is the title the player page shows.
func title(app *nsgotest.App) string { return app.Find("id:player >> id:title").Text }

func TestVideoPagePlaysParts(t *testing.T) {
	data := t.TempDir()
	app, pl := startDemo(t, data)
	app.Press("id:nav-demo-home")
	app.WaitFor("id:item-video-v1", wait)
	app.Press("id:item-video-v1")
	app.WaitFor("id:video-page", wait)
	waitText(app, "description", "Hill Walker")
	app.Find("id:part-2 >> text:Summit")
	app.Find("id:item-video-v2")

	app.Press("id:part-1")
	app.WaitFor("id:player", wait)
	app.WaitUntil(wait, func() bool { return title(app) == "Sunrise over the hills – Climb" })
	src := pl.playing.GetUntracked()
	if src.Quality != "720" || len(src.Qualities) != 3 || src.Audio == nil || src.Headers.Get("Referer") == "" || len(src.Subtitles) != 3 || src.Subtitles[2].Text == "" {
		t.Fatalf("source = %+v", src)
	}

	// Another quality calls stream again and starts where the part was.
	app.Post(func() { pl.switchQuality("480", 42*time.Second) })
	app.WaitUntil(wait, func() bool { return pl.playing.GetUntracked().Quality == "480" })
	if src := pl.playing.GetUntracked(); src.Start != 42*time.Second || !strings.Contains(src.URL, "/480.m4s") {
		t.Fatalf("source at 480p = %+v", src)
	}
	app.Find("id:quality")

	// At the end, the next part plays at the same quality.
	app.Post(pl.next)
	app.WaitUntil(wait, func() bool { return title(app) == "Sunrise over the hills – Summit" })
	if src := pl.playing.GetUntracked(); !strings.Contains(src.URL, "/v1/2/480.m4s") || src.Start != 0 {
		t.Fatalf("next part = %+v", src)
	}

	// Positions reach the plugin.
	app.Post(func() { pl.report(pl.playing.GetUntracked().Key, 30*time.Second, 47*time.Second) })
	app.WaitUntil(wait, func() bool {
		raw, _ := os.ReadFile(filepath.Join(data, "plugin-data", "demo", "storage.json"))
		return strings.Contains(string(raw), `"progress:v1"`)
	})

	app.Press("id:back")
	app.WaitForGone("id:player", time.Second)
	app.Find("id:video-page")
}

func TestPlayerCoversTheWindowOverALongPage(t *testing.T) {
	app, _ := startDemo(t, "")
	app.Resize(1100, 300)
	app.Press("id:nav-demo-home")
	app.WaitFor("id:item-video-v1", wait)
	app.Press("id:item-video-v1")
	app.WaitFor("id:play-video", wait)
	app.Press("id:play-video")
	if h := app.WaitFor("id:player", wait).Frame.Size.Height; h != 300 {
		t.Errorf("player is %vpt high in a 300pt window", h)
	}
}

func TestStreamErrorsShowInAToast(t *testing.T) {
	app, pl := startDemo(t, "")
	app.Press("id:nav-demo-home")
	app.WaitFor("id:item-video-v2", wait)
	app.Press("id:item-video-v2")
	app.WaitFor("id:play-video", wait)
	app.Press("id:play-video")
	app.WaitFor("id:player", wait)
	app.Post(func() { pl.switchQuality("1080", 0) })
	app.WaitFor("text:1080p needs an API key in the settings", wait)

	// Setup opens the settings.
	app.Press("text:Open Settings")
	app.WaitUntil(wait, func() bool { return pl.sh.top().kind == routePlugin })
}

// choose picks the option at index, labelled label, of the select id.
func choose(app *nsgotest.App, id string, index int, label string) {
	app.Press("id:" + id)
	if option := fmt.Sprintf("id:%s-%d", id, index); app.Exists(option) {
		app.Press(option)
	} else {
		// On macOS, the list is the system's.
		app.ChoosePopup(label)
	}
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestManagePlugins(t *testing.T) {
	data := t.TempDir()
	settings := filepath.Join(data, "plugin-data", "demo", "settings.json")

	// Installing from a folder asks to approve the hosts first.
	app, pl := start(t, data)
	app.Press("id:nav-settings")
	app.WaitFor("id:add-plugin", wait)
	// The pickers' answers, which nsgotest gives only headless.
	pick := func(open func() (fs.FS, error)) { app.Post(func() { pl.sh.inspect(pl.sh.cx, open, "") }) }
	pick(func() (fs.FS, error) { return demo(), nil })
	app.WaitFor("id:install >> text:Install Demo 1.0.0?", wait)
	app.Find("id:install >> text:demo.example.com")
	app.Press("id:confirm-install")
	// Installed, its home opens.
	app.WaitFor("id:item-video-v1", wait)
	app.Press("id:nav-settings")
	waitText(app, "plugin-source-demo", "demo.example.com")

	// Its settings, on its page, reach the plugin's data.
	app.Press("id:plugin-row-demo")
	app.WaitFor("id:network-demo >> text:demo.example.com", wait)
	choose(app, "setting-demo-quality", 2, "480p")
	app.WaitUntil(wait, func() bool {
		raw, _ := os.ReadFile(settings)
		return strings.Contains(string(raw), `"quality":"480"`)
	})
	app.Type("id:setting-demo-apiKey", "secret")
	app.Submit("id:setting-demo-apiKey")
	app.WaitUntil(wait, func() bool {
		raw, _ := os.ReadFile(settings)
		return strings.Contains(string(raw), `"apiKey":"secret"`)
	})

	// Installing it again is refused.
	pick(func() (fs.FS, error) { return demo(), nil })
	app.WaitFor("text:The plugin cannot be installed", wait)

	// Disabled, it leaves the sidebar, after a restart too.
	app.Press("id:page-enable-demo")
	app.WaitForGone("id:nav-demo-home", wait)
	app, _ = start(t, data)
	if app.Exists("id:nav-demo-home") {
		t.Fatal("a disabled plugin is in the sidebar after a restart")
	}

	// Enabled again, it returns with its settings.
	app.Press("id:nav-settings")
	app.Press("id:enable-demo")
	app.WaitFor("id:nav-demo-home", wait)
	app.Press("id:plugin-row-demo")
	app.WaitFor("id:settings-demo >> text:480p", wait)
	app, _ = start(t, data)
	app.Find("id:nav-demo-home")

	// Removed, it goes with its data.
	app.Press("id:nav-settings")
	app.Press("id:plugin-row-demo")
	app.Press("id:remove-demo")
	app.WaitFor("id:remove >> text:Remove Demo?", wait)
	app.Press("id:confirm-remove")
	app.WaitForGone("id:plugin-row-demo", wait)
	app.WaitForGone("id:nav-demo-home", wait)
	app.WaitUntil(wait, func() bool { return !exists(filepath.Join(data, "plugins", "demo")) && !exists(filepath.Dir(settings)) })
	app, pl = start(t, data)
	app.Press("id:nav-settings")
	app.WaitFor("id:add-plugin", wait)
	if app.Exists("id:plugin-row-demo") {
		t.Fatal("a removed plugin is listed after a restart")
	}

	// A zip installs it again, its settings back to their defaults.
	path := zipOf(t, demoFiles(t))
	pick(func() (fs.FS, error) { return readZip(path) })
	app.WaitFor("id:install", wait)
	app.Press("id:confirm-install")
	app.WaitFor("id:item-video-v1", wait)
	app.Press("id:nav-settings")
	app.Press("id:plugin-row-demo")
	app.WaitFor("id:settings-demo >> text:720p", wait)
}

// demoFiles are the demo plugin's files, in a folder of the zip.
func demoFiles(t *testing.T) fstest.MapFS {
	files := fstest.MapFS{}
	err := fs.WalkDir(demo(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(demo(), path)
		files["demo-plugin/"+path] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestLanguageChoice(t *testing.T) {
	data := t.TempDir()
	app, _ := startDemo(t, data)
	app.Press("id:nav-settings")
	choose(app, "language", 2, "简体中文")
	app.WaitFor("id:nav-settings >> text:设置", wait)
	app.WaitFor("id:nav-demo-popular >> text:热门", wait)

	app, _ = start(t, data)
	app.Find("id:nav-settings >> text:设置")
	app.Find("id:nav-demo-popular >> text:热门")
	app.Press("id:nav-settings")
	choose(app, "language", 0, "System")
	app.WaitFor("id:nav-settings >> text:Settings", wait)
}

func TestStatsSwitch(t *testing.T) {
	data := t.TempDir()
	app, _ := start(t, data)
	app.Press("id:nav-settings")
	app.Press("id:stats")
	path := filepath.Join(data, "settings.json")
	app.WaitUntil(wait, func() bool {
		s, err := store.OpenSettings(path)
		return err == nil && !s.Stats()
	})
}

// TestLocalFilesPlay plays a clip of NSGo Media's testdata from the
// local files plugin's home.
func TestLocalFilesPlay(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	clips := filepath.Join(filepath.Dir(file), "../../../testdata")
	data := t.TempDir()
	p, err := local.Load(plugin.Options{AppVersion: Version, DataDir: filepath.Join(data, "plugin-data")})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetSetting(context.Background(), "folders", clips); err != nil {
		t.Fatal(err)
	}
	p.Close()

	var pl *player
	a := App{Builtin: []Builtin{local.Load}, DataDir: data, Client: &http.Client{Transport: offline{}}, started: func(p *player) { pl = p }}
	app := nsgotest.Start(t, a.Render, nsgotest.Size(1100, 700), nsgotest.Environment(core.Environment{Locale: "en-US"}))
	app.Press("id:nav-local-home")
	video := filepath.Join(clips, "av.mkv")
	app.WaitFor("id:item-video-"+video, wait)
	app.Press("id:item-video-" + video)
	app.WaitFor("id:video-page", wait)
	app.Press("id:play-video")
	app.WaitFor("id:player", wait)
	app.WaitUntil(wait, func() bool { s := pl.playing.GetUntracked(); return s != nil && s.URL == video })
}

// TestUsePluginFromPath uses the demo plugin where it is, from the folder
// picked in the settings.
func TestUsePluginFromPath(t *testing.T) {
	data := t.TempDir()
	app, pl := start(t, data)
	app.Press("id:nav-settings")
	// The picker's answer, which nsgotest gives only headless.
	app.Post(func() { pl.sh.link(pl.sh.cx, demoDir()) })
	app.WaitFor("id:install >> text:Use Demo 1.0.0 from its folder?", wait)
	app.Press("id:confirm-install")
	app.WaitFor("id:nav-demo-home", wait)
	if p := pl.sh.plugins.GetUntracked(); len(p) != 1 {
		t.Fatalf("plugins %v", p)
	} else if dir, ok := pl.sh.library.linkedDir(p[0]); !ok || dir != filepath.Clean(demoDir()) {
		t.Fatalf("linked folder %q", dir)
	}
	if exists(filepath.Join(data, "plugins", "demo")) {
		t.Fatal("a plugin used from its path was installed")
	}

	// It stays after a restart, and removed, only its link and data go.
	app, _ = start(t, data)
	app.Press("id:nav-settings")
	waitText(app, "plugin-source-demo", filepath.Base(demoDir()))
	app.Press("id:plugin-row-demo")
	app.Press("id:remove-demo")
	app.WaitFor("id:remove >> text:Its settings and data are removed; its folder stays.", wait)
	app.Press("id:confirm-remove")
	app.WaitForGone("id:plugin-row-demo", wait)
	if !exists(filepath.Join(demoDir(), "plugin.yaml")) {
		t.Fatal("removing a plugin used from its path removed its folder")
	}
	app, _ = start(t, data)
	app.Press("id:nav-settings")
	app.WaitFor("id:add-plugin", wait)
	if app.Exists("id:plugin-row-demo") {
		t.Fatal("a removed plugin is listed after a restart")
	}
}

// TestRefusedPluginsStayListed lists a plugin the app refused, with why,
// until the viewer removes it.
func TestRefusedPluginsStayListed(t *testing.T) {
	data := t.TempDir()
	newer := fstest.MapFS{
		"plugin.yaml": {Data: []byte("id: later\nname: Later\nversion: 2.0.0\nminAppVersion: 9.0.0\nmain: index.ts\nmedia: video\n")},
		"index.ts":    {Data: []byte("export function home() { return { sections: [] } }")},
	}
	if err := store.InstallPlugin(filepath.Join(data, "plugins"), "later", newer); err != nil {
		t.Fatal(err)
	}
	app, _ := start(t, data)
	app.Press("id:nav-settings")
	app.WaitFor("id:refused-row-later >> text:Needs NSGo Player 9.0.0", wait)
	app.Press("id:refused-row-later")
	app.WaitFor("id:refused >> text:It needs NSGo Player 9.0.0 or later. Update the app to use it.", wait)
	app.Press("id:remove-refused")
	app.WaitFor("id:remove >> text:Remove Later?", wait)
	app.Press("id:confirm-remove")
	app.WaitForGone("id:refused-row-later", wait)
	app.WaitUntil(wait, func() bool { return !exists(filepath.Join(data, "plugins", "later")) })
}

// TestSearchKindsAndNoResults searches one kind of results, and says when
// nothing matches.
func TestSearchKindsAndNoResults(t *testing.T) {
	app, _ := startDemo(t, "")
	app.Press("id:nav-demo-search")
	waitText(app, "state-title", "Search Demo")
	app.Type("id:query", "hill")
	app.Submit("id:query")
	app.WaitFor("id:item-uploader-u1", wait)
	app.Press("id:kind-video")
	app.WaitForGone("id:item-uploader-u1", wait)
	app.WaitFor("id:item-video-v1", wait)

	app.Clear("id:query")
	app.Type("id:query", "nothing like it")
	app.Submit("id:query")
	waitText(app, "state-title", "No results")
	app.Press("id:clear-query")
	waitText(app, "state-title", "Search Demo")
}
