package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
	"github.com/nsgo-dev/nsgo-media/app/internal/store"
)

// fakeGitHub stands in for GitHub's REST API and its release downloads.
type fakeGitHub struct {
	*httptest.Server
	mu sync.Mutex
	// repos are the repositories by owner/repo, with their latest
	// release, nil for none.
	repos map[string]*fakeRelease
	// zips are the release assets by name.
	zips    map[string][]byte
	limited bool
	// asked counts the requests for latest releases.
	asked int
}

type fakeRelease struct {
	tag, body  string
	published  time.Time
	prerelease bool
	// assets are the names of its files, of zips or not.
	assets []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{repos: map[string]*fakeRelease{}, zips: map[string][]byte{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// publish serves data as the release asset name.
func (g *fakeGitHub) publish(name string, data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.zips[name] = data
}

func (g *fakeGitHub) set(repo string, r *fakeRelease) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repos[repo] = r
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if name, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
		data, ok := g.zips[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
		return
	}
	if g.limited {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	path, latest := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/repos/"), "/releases/latest")
	rel, ok := g.repos[path]
	if !ok || (latest && rel == nil) {
		http.NotFound(w, r)
		return
	}
	if !latest {
		json.NewEncoder(w).Encode(map[string]any{"full_name": path})
		return
	}
	g.asked++
	type asset struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int    `json:"size"`
	}
	assets := []asset{}
	for _, name := range rel.assets {
		assets = append(assets, asset{name, g.URL + "/download/" + name, len(g.zips[name])})
	}
	json.NewEncoder(w).Encode(map[string]any{"tag_name": rel.tag, "body": rel.body, "prerelease": rel.prerelease,
		"published_at": rel.published.Format(time.RFC3339), "assets": assets})
}

// zipBytes zips files.
func zipBytes(t *testing.T, files fstest.MapFS) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if err := w.AddFS(files); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// demoRelease is the demo plugin at version, reaching hosts, in a
// folder of a zip as zipping its folder makes.
func demoRelease(t *testing.T, version, minApp string, hosts ...string) []byte {
	files := demoFiles(t)
	m := string(files["demo-plugin/plugin.yaml"].Data)
	m = strings.Replace(m, "version: 1.0.0", "version: "+version, 1)
	m = strings.Replace(m, "minAppVersion: 0.1.0", "minAppVersion: "+minApp, 1)
	if len(hosts) > 0 {
		m = strings.Replace(m, "network: [demo.example.com]", "network: ["+strings.Join(hosts, ", ")+"]", 1)
	}
	files["demo-plugin/plugin.yaml"] = &fstest.MapFile{Data: []byte(m)}
	return zipBytes(t, files)
}

func TestParseRepo(t *testing.T) {
	for in, want := range map[string]string{
		"owner/repo":                                "owner/repo",
		"  owner/repo.js ":                          "owner/repo.js",
		"https://github.com/owner/repo":             "owner/repo",
		"https://github.com/owner/repo.git":         "owner/repo",
		"https://www.github.com/owner/repo/":        "owner/repo",
		"github.com/owner/repo/releases/tag/v1.0.0": "owner/repo",
		"https://github.com/owner/repo?tab=readme":  "owner/repo",
		"owner":                         "",
		"owner/repo/more":               "",
		"https://gitlab.com/owner/repo": "",
		"https://github.com/owner":      "",
		"own er/repo":                   "",
		"owner/..":                      "",
		"":                              "",
	} {
		got, ok := parseRepo(in)
		if got != want || ok != (want != "") {
			t.Errorf("parseRepo(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestGitHubFindsTheLatestReleasesPlugin(t *testing.T) {
	g := newFakeGitHub(t)
	gh := &gitHub{api: g.URL, client: http.DefaultClient}
	l := testLibrary(t)
	published := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	g.publish("source.zip", zipBytes(t, fstest.MapFS{"README.md": {Data: []byte("not a plugin")}}))
	g.publish("demo.zip", demoRelease(t, "1.0.0", "0.1.0"))
	g.publish("later.zip", demoRelease(t, "2.0.0", "9.0.0"))
	g.set("example/empty", nil)
	g.set("example/notes", &fakeRelease{tag: "v1", assets: []string{"notes.txt"}})
	g.set("example/source", &fakeRelease{tag: "v1", assets: []string{"source.zip"}})
	g.set("example/beta", &fakeRelease{tag: "v1", prerelease: true, assets: []string{"demo.zip"}})
	g.set("example/later", &fakeRelease{tag: "v2.0.0", assets: []string{"later.zip"}})
	g.set("example/demo", &fakeRelease{tag: "v1.0.0", body: "First.", published: published, assets: []string{"notes.txt", "source.zip", "demo.zip"}})

	find := func(repo string) (*candidate, error) {
		r, err := gh.latest(context.Background(), repo)
		if err != nil {
			return nil, err
		}
		return l.inspectRelease(context.Background(), gh, r, func(string) bool { return false })
	}
	for repo, want := range map[string]gitHubProblem{
		"example/missing": problemNoRepo,
		"example/empty":   problemNoRelease,
		"example/beta":    problemNoRelease,
		"example/notes":   problemNoZip,
		"example/source":  problemNoZip,
	} {
		var ge *gitHubError
		if _, err := find(repo); !errors.As(err, &ge) || ge.problem != want {
			t.Errorf("%s: err = %v, want problem %d", repo, err, want)
		}
	}
	if _, err := find("example/later"); !errors.As(err, new(*plugin.TooNewError)) {
		t.Errorf("a release needing a newer app: err = %v", err)
	}

	// The zip holding a plugin is the plugin, whichever comes first.
	c, err := find("example/demo")
	if err != nil {
		t.Fatal(err)
	}
	if c.manifest.ID != "demo" || c.from != "example/demo" || c.release.Tag != "v1.0.0" || !c.release.Published.Equal(published) || c.release.notes != "First." {
		t.Fatalf("candidate = %+v, release %+v", c, c.release)
	}

	// Installed, the settings remember its release; removed, they forget
	// it.
	p, err := l.install(c)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := l.releaseOf(p); !ok || r.Repo != "example/demo" || r.Tag != "v1.0.0" {
		t.Fatalf("release kept = %+v, %v", r, ok)
	}
	if err := l.remove(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.settings.Release("demo"); ok {
		t.Fatal("a removed plugin's release is kept")
	}

	// GitHub limiting requests says so.
	g.limited = true
	var ge *gitHubError
	if _, err := find("example/demo"); !errors.As(err, &ge) || ge.problem != problemLimited {
		t.Fatalf("limited: err = %v", err)
	}
	// One that cannot be reached says so.
	gh.api = "http://127.0.0.1:1"
	if _, err := find("example/demo"); !errors.As(err, &ge) || ge.problem != problemUnreachable {
		t.Fatalf("unreachable: err = %v", err)
	}
}

func TestLibraryUpdateKeepsData(t *testing.T) {
	l := testLibrary(t)
	inspect := func(version string, files fstest.MapFS) *candidate {
		c, err := l.inspect(files, func(string) bool { return false })
		if err != nil {
			t.Fatal(err)
		}
		c.release = &release{Release: store.Release{Repo: "example/p", Tag: "v" + version}}
		return c
	}
	v1 := fstest.MapFS{"plugin.yaml": {Data: []byte(`{"id":"p","name":"P","version":"1.0.0","minAppVersion":"0.1.0","main":"main.ts","media":"video"}`)}, "main.ts": homeSource}
	p, err := l.install(inspect("1.0.0", v1))
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(l.opts.DataDir, "p", "storage.json")
	if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte(`{"k":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	v2 := fstest.MapFS{"plugin.yaml": {Data: []byte(`{"id":"p","name":"P","version":"1.1.0","minAppVersion":"0.1.0","main":"main.ts","media":"video"}`)}, "main.ts": homeSource}
	p2, err := l.update(p, inspect("1.1.0", v2))
	if err != nil {
		t.Fatal(err)
	}
	if p2.Manifest.Version != "1.1.0" {
		t.Fatalf("updated to %s", p2.Manifest.Version)
	}
	if raw, _ := os.ReadFile(data); string(raw) != `{"k":1}` {
		t.Fatalf("data after the update = %s", raw)
	}
	if r, _ := l.settings.Release("p"); r.Tag != "v1.1.0" {
		t.Fatalf("release kept = %+v", r)
	}

	// One that fails to load leaves the version installed, loaded again.
	broken := fstest.MapFS{"plugin.yaml": v2["plugin.yaml"], "main.ts": {Data: []byte(`import x from "./x.js"; export const home = x`)}}
	c := &candidate{fsys: broken, manifest: p2.Manifest, release: &release{Release: store.Release{Repo: "example/p", Tag: "v1.2.0"}}}
	p3, err := l.update(p2, c)
	if err == nil || p3 == nil || p3.Manifest.Version != "1.1.0" {
		t.Fatalf("a broken update = %v, %v", p3, err)
	}
	if r, _ := l.settings.Release("p"); r.Tag != "v1.1.0" {
		t.Fatalf("release kept after a failed update = %+v", r)
	}
	if entries, _ := os.ReadDir(l.dir); len(entries) != 1 {
		t.Fatalf("plugins folder holds %v", entries)
	}
	p3.Close()
}

func TestInstallAndUpdateFromGitHub(t *testing.T) {
	g := newFakeGitHub(t)
	g.publish("demo-1.0.0.zip", demoRelease(t, "1.0.0", "0.1.0"))
	g.publish("later.zip", demoRelease(t, "2.0.0", "9.0.0"))
	g.set("example/empty", nil)
	g.set("example/notes", &fakeRelease{tag: "v1", assets: []string{"notes.txt"}})
	g.set("example/later", &fakeRelease{tag: "v2.0.0", assets: []string{"later.zip"}})
	g.set("example/demo", &fakeRelease{tag: "v1.0.0", published: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC), assets: []string{"demo-1.0.0.zip"}})
	data := t.TempDir()
	settings := filepath.Join(data, "plugin-data", "demo", "settings.json")

	app, _ := startWith(t, data, g.URL)
	app.Press("id:nav-settings")
	// Without a plugin added, the list offers GitHub.
	app.WaitFor("id:no-plugins", wait)
	app.Press("id:empty-install-github")
	app.WaitFor("id:from-github >> text:Install from GitHub", wait)
	try := func(repo, want string) {
		t.Helper()
		app.Clear("id:repository")
		app.Type("id:repository", repo)
		app.Press("id:find-release")
		waitText(app, "from-github-error", want)
	}
	try("not a repo", "Type a repository as owner/repo")
	try("example/missing", "There is no repository example/missing on GitHub.")
	try("example/empty", "It has no release yet.")
	try("https://github.com/example/notes", "Its latest release has no plugin zip.")
	app.Screenshot("from-github-no-zip")
	try("example/later", "It needs NSGo Player 9.0.0 or later.")

	// The release's plugin asks to approve its hosts, then installs.
	app.Clear("id:repository")
	app.Type("id:repository", "https://github.com/example/demo")
	app.Submit("id:repository")
	app.WaitFor("id:install >> text:Install Demo 1.0.0?", wait)
	app.Find("id:install >> text:example/demo · Released Nov 22, 2026")
	app.Find("id:install >> text:demo.example.com")
	app.Screenshot("approve-from-github")
	app.Press("id:confirm-install")
	app.WaitFor("id:item-video-v1", wait)
	app.Press("id:nav-settings")
	waitText(app, "plugin-source-demo", "example/demo")
	app.Find("id:check-updates")
	if app.Exists("id:update-demo") || app.Exists("id:updates-badge") {
		t.Fatal("the release installed is offered as an update")
	}

	// A setting the update keeps.
	app.Press("id:plugin-row-demo")
	waitText(app, "repository-link", "example/demo")
	choose(app, "setting-demo-quality", 2, "480p")
	app.WaitUntil(wait, func() bool {
		raw, _ := os.ReadFile(settings)
		return strings.Contains(string(raw), `"quality":"480"`)
	})
	app.Press("id:page-back")

	// A newer release that needs a newer app is offered, and refused.
	g.publish("demo-1.1.0.zip", demoRelease(t, "1.1.0", "9.0.0"))
	g.set("example/demo", &fakeRelease{tag: "v1.1.0", assets: []string{"demo-1.1.0.zip"}})
	app.Press("id:check-updates")
	app.WaitFor("id:update-demo >> text:Update to 1.1.0", wait)
	waitText(app, "checked", "Checked just now")
	app.Find("id:updates-badge")
	app.Press("id:update-demo")
	app.WaitFor("id:update-error >> text:It needs NSGo Player 9.0.0 or later. Update the app to install it.", wait)
	if app.Exists("id:confirm-update") {
		t.Fatal("an update needing a newer app can be tried again")
	}
	app.Press("id:cancel-update")
	app.WaitForGone("id:update", wait)
	if app.Exists("id:plugin-page") {
		t.Fatal("pressing the update opened the plugin's page")
	}

	// A newer one adding a host shows its notes and asks to approve it.
	g.publish("demo-1.2.0.zip", demoRelease(t, "1.2.0", "0.1.0", "demo.example.com", "cdn.example.org"))
	g.set("example/demo", &fakeRelease{tag: "v1.2.0", body: "Faster pages.", published: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC), assets: []string{"demo-1.2.0.zip"}})
	app.Press("id:check-updates")
	app.WaitFor("id:update-demo >> text:Update to 1.2.0", wait)
	app.Screenshot("settings-update")
	app.Press("id:plugin-row-demo")
	app.WaitFor("id:update-banner >> text:Version 1.2.0 is available", wait)
	app.Screenshot("plugin-page-update")
	app.Press("id:page-update")
	app.WaitFor("id:update >> text:Update Demo to 1.2.0?", wait)
	app.Find("id:update >> text:You have 1.0.0 · Released Nov 23, 2026")
	app.Find("id:release-notes >> text:Faster pages.")
	app.Find("id:new-host-cdn.example.org")
	app.Find("id:update >> text:Approve and Update")
	app.Screenshot("update-new-hosts")
	app.Press("id:confirm-update")
	app.WaitForGone("id:update", wait)

	// Updated, its page shows the new version with its settings.
	app.WaitFor("id:plugin-page >> text:Version 1.2.0", wait)
	app.Find("id:settings-demo >> text:480p")
	app.WaitForGone("id:update-banner", wait)
	if app.Exists("id:updates-badge") {
		t.Fatal("the badge stays after updating")
	}
	if raw, _ := os.ReadFile(settings); !strings.Contains(string(raw), `"quality":"480"`) {
		t.Fatalf("settings after the update: %s", raw)
	}

	// After a restart, it checks again and finds nothing newer.
	asked := func() int { g.mu.Lock(); defer g.mu.Unlock(); return g.asked }
	before := asked()
	app, _ = startWith(t, data, g.URL)
	app.WaitUntil(wait, func() bool { return asked() > before })
	app.Press("id:nav-settings")
	waitText(app, "plugin-source-demo", "example/demo")
	waitText(app, "checked", "Checked")
	if app.Exists("id:update-demo") {
		t.Fatal("the version installed is offered after a restart")
	}

	// A check that fails says why; the plugin works as it is.
	g.mu.Lock()
	g.limited = true
	g.mu.Unlock()
	app.Press("id:check-updates")
	app.WaitFor("id:check-failed >> text:Could not check for updates: example/demo: GitHub limits requests now. Try again later.", wait)
	app.Find("id:plugin-row-demo")
	app.Screenshot("check-failed")
}
