package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// fakeUI records what plugins ask the app to show.
type fakeUI struct {
	mu      sync.Mutex
	shown   []SignInPrompt
	closed  int
	opened  []string
	dismiss bool
}

func (u *fakeUI) ShowSignIn(_ *Plugin, p SignInPrompt) {
	u.mu.Lock()
	u.shown = append(u.shown, p)
	dismiss := u.dismiss
	u.mu.Unlock()
	if dismiss {
		go p.Dismissed()
	}
}

func (u *fakeUI) CloseSignIn(*Plugin) {
	u.mu.Lock()
	u.closed++
	u.mu.Unlock()
}

func (u *fakeUI) OpenURL(url string) error {
	u.mu.Lock()
	u.opened = append(u.opened, url)
	u.mu.Unlock()
	return nil
}

func options(ui UI) Options {
	return Options{AppVersion: "0.1.0", Language: "en", UI: ui, Logger: slog.New(slog.DiscardHandler)}
}

func loadDemo(t *testing.T, opts Options) *Plugin {
	t.Helper()
	p, err := Load(os.DirFS("../../examples/demo-plugin"), false, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// inline loads a plugin of one JavaScript module with the manifest's
// fields after main.
func inline(t *testing.T, opts Options, manifest, src string) *Plugin {
	t.Helper()
	fsys := fstest.MapFS{
		"plugin.yaml": {Data: []byte(`{"id":"test","name":"Test","version":"1.0.0","minAppVersion":"0.1.0","main":"main.js","media":"video"` + manifest + `}`)},
		"main.js":     {Data: []byte(src)},
	}
	p, err := Load(fsys, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestDemoExports(t *testing.T) {
	ctx := context.Background()
	ui := &fakeUI{}
	p := loadDemo(t, options(ui))
	for _, e := range Exports {
		if !p.Has(e) {
			t.Errorf("the demo plugin does not export %s", e)
		}
	}

	if p.Manifest.Color != "#e8553a" || !strings.Contains(string(p.Icon), "<svg") {
		t.Fatalf("icon %q, color %q", p.Icon, p.Manifest.Color)
	}
	dm := must[[]Danmaku](t)(p.Danmaku(ctx, DanmakuParams{Video: "v1", Part: "1", From: 0, To: 10}))
	if len(dm) != 20 || dm[0].Mode != DanmakuTop || dm[0].Color != "#66ccff" || dm[1].Text == "" {
		t.Fatalf("danmaku = %+v", dm)
	}

	if a := must[*Account](t)(p.Account(ctx)); a != nil {
		t.Fatalf("account before signing in = %+v", a)
	}
	a := must[*Account](t)(p.SignIn(ctx))
	if a.Name != "Demo Viewer" || len(ui.shown) != 1 || ui.shown[0].QRCode == "" || ui.closed == 0 {
		t.Fatalf("signIn = %+v, prompts %+v, closed %d", a, ui.shown, ui.closed)
	}
	if a := must[*Account](t)(p.Account(ctx)); a == nil || a.ID != "demo" {
		t.Fatalf("account after signing in = %+v", a)
	}

	home := must[*Page](t)(p.Home(ctx, PageParams{}))
	if len(home.Sections) != 4 || home.Sections[0].Title != "Popular" || home.Next != "2" {
		t.Fatalf("home = %+v", home)
	}
	if v := home.Sections[0].Items[0]; v.Kind != KindVideo || v.Image == nil || v.Image.Headers["Referer"] == "" {
		t.Fatalf("home's first video = %+v", v)
	}
	if next := must[*Page](t)(p.Home(ctx, PageParams{Cursor: home.Next})); next.Sections[0].Items[0].Kind != KindCollection {
		t.Fatalf("home's next page = %+v", next)
	}
	for id, n := range map[string]int{"popular": 3, "favorites": 1, "u1": 2, "c1": 2} {
		pg := must[*Page](t)(p.Browse(ctx, BrowseParams{ID: id}))
		if len(pg.Sections[0].Items) != n {
			t.Errorf("browse %s = %+v", id, pg)
		}
	}

	res := must[*Page](t)(p.Search(ctx, SearchParams{Query: "hill"}))
	if len(res.Sections) != 2 || res.Sections[0].Items[0].ID != "v1" || res.Sections[1].Items[0].ID != "u1" {
		t.Fatalf("search = %+v", res)
	}
	if res := must[*Page](t)(p.Search(ctx, SearchParams{Query: "hill", Kind: KindUploader})); len(res.Sections) != 1 {
		t.Fatalf("search of uploaders = %+v", res)
	}
	if s := must[[]string](t)(p.Suggest(ctx, SuggestParams{Query: "fog"})); len(s) != 1 || s[0] != "Fog in the valley" {
		t.Fatalf("suggest = %v", s)
	}
	if it := must[*Item](t)(p.Resolve(ctx, ResolveParams{Text: "https://demo.example.com/video/v2"})); it == nil || it.ID != "v2" {
		t.Fatalf("resolve of a link = %+v", it)
	}
	if it := must[*Item](t)(p.Resolve(ctx, ResolveParams{Text: "https://demo.example.com/video/v1?part=2"})); it == nil || it.ID != "v1?part=2" {
		t.Fatalf("resolve of a link to a part = %+v", it)
	}
	if v := must[*Video](t)(p.Video(ctx, VideoParams{ID: "v1?part=2"})); v.Part != "2" {
		t.Fatalf("video of a link to a part starts at %q", v.Part)
	}
	if it := must[*Item](t)(p.Resolve(ctx, ResolveParams{Text: "https://elsewhere.example.com/video/v2"})); it != nil {
		t.Fatalf("resolve of another site's link = %+v", it)
	}

	v := must[*Video](t)(p.Video(ctx, VideoParams{ID: "v1"}))
	if len(v.Parts) != 2 || v.Uploader == nil || v.Published == nil || v.Published.Year() != 2026 || len(v.Related) != 2 {
		t.Fatalf("video = %+v", v)
	}
	s := must[*Stream](t)(p.Stream(ctx, StreamParams{Video: "v1", Part: v.Parts[0].ID}))
	if s.Quality != "720" || s.Video == nil || s.Audio == nil || s.Video.Headers["Referer"] == "" || len(s.Qualities) != 3 || len(s.Subtitles) != 3 || s.Subtitles[2].Text == "" {
		t.Fatalf("stream = %+v", s)
	}
	if !strings.HasSuffix(s.Video.URL, "sign=a9520efc44668c1b3f5e995984eb75c6") {
		t.Fatalf("stream's video URL %s is not signed with the MD5 of v1/1/720", s.Video.URL)
	}

	if err := p.Progress(ctx, ProgressParams{Video: "v1", Part: "1", Position: 12, Duration: 47.5}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := p.data.get("progress:v1"); string(raw) != `{"part":"1","position":12}` {
		t.Fatalf("progress stored %s", raw)
	}
	if err := p.SetSetting(ctx, "quality", "480"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := p.data.get("changed"); string(raw) != `{"key":"quality","value":"480"}` {
		t.Fatalf("setSetting stored %s", raw)
	}
	if s := must[*Stream](t)(p.Stream(ctx, StreamParams{Video: "v1", Part: "1"})); s.Quality != "480" {
		t.Fatalf("stream after the default quality changed = %+v", s)
	}

	if err := p.SignOut(ctx); err != nil {
		t.Fatal(err)
	}
	if a := must[*Account](t)(p.Account(ctx)); a != nil {
		t.Fatalf("account after signing out = %+v", a)
	}
}

func TestDemoFollowing(t *testing.T) {
	ctx := context.Background()
	p := loadDemo(t, options(&fakeUI{}))

	// Signed out, following needs signing in, and a show is not followed.
	if _, err := p.Following(ctx, FollowingParams{}); !errors.Is(err, ErrSignIn) {
		t.Fatalf("following signed out = %v", err)
	}
	if err := p.Follow(ctx, FollowParams{ID: "s1", Status: "watching"}); !errors.Is(err, ErrSignIn) {
		t.Fatalf("follow signed out = %v", err)
	}
	v := must[*Video](t)(p.Video(ctx, VideoParams{ID: "v4"}))
	if v.Show == nil || v.Show.ID != "s1" || v.Show.Status != "" || len(v.Parts) != 4 {
		t.Fatalf("a show's video = %+v", v)
	}
	if v := must[*Video](t)(p.Video(ctx, VideoParams{ID: "v1"})); v.Show != nil {
		t.Fatalf("a video of no show belongs to %+v", v.Show)
	}

	must[*Account](t)(p.SignIn(ctx))
	if f := must[*Following](t)(p.Following(ctx, FollowingParams{})); len(f.Shows) != 0 {
		t.Fatalf("following before following anything = %+v", f)
	}
	if err := p.Follow(ctx, FollowParams{ID: "s2", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Follow(ctx, FollowParams{ID: "s1", Status: "watching"}); err != nil {
		t.Fatal(err)
	}
	f := must[*Following](t)(p.Following(ctx, FollowingParams{}))
	if len(f.Shows) != 2 || f.Shows[0].ID != "s1" || !f.Shows[0].New || f.Shows[0].Watched != 2 || f.Shows[0].Episodes != 4 || f.Shows[0].Video != "v4" || f.Shows[1].New {
		t.Fatalf("following = %+v", f)
	}
	if f := must[*Following](t)(p.Following(ctx, FollowingParams{Status: "completed"})); len(f.Shows) != 1 || f.Shows[0].ID != "s2" {
		t.Fatalf("following of one status = %+v", f)
	}
	if v := must[*Video](t)(p.Video(ctx, VideoParams{ID: "v4"})); v.Show.Status != "watching" || v.Show.Watched != 2 {
		t.Fatalf("a followed show's video = %+v", v.Show)
	}

	// An episode played to its end counts as watched.
	if err := p.Progress(ctx, ProgressParams{Video: "v4", Part: "3", Position: 295, Duration: 300}); err != nil {
		t.Fatal(err)
	}
	if v := must[*Video](t)(p.Video(ctx, VideoParams{ID: "v4"})); v.Show.Watched != 3 {
		t.Fatalf("watched after an episode = %d", v.Show.Watched)
	}

	// A status the manifest lacks never reaches the plugin.
	if err := p.Follow(ctx, FollowParams{ID: "s1", Status: "dropped"}); err == nil || errors.Is(err, ErrSignIn) {
		t.Fatalf("follow with a status the manifest lacks = %v", err)
	}
	if err := p.Unfollow(ctx, UnfollowParams{ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if f := must[*Following](t)(p.Following(ctx, FollowingParams{})); len(f.Shows) != 1 || f.Shows[0].ID != "s2" {
		t.Fatalf("following after unfollowing = %+v", f)
	}
}

func TestDemoErrorCodes(t *testing.T) {
	ctx := context.Background()
	p := loadDemo(t, options(&fakeUI{}))
	check := func(err, want error) {
		t.Helper()
		var e *Error
		if !errors.Is(err, want) || !errors.As(err, &e) || e.Message == "" {
			t.Fatalf("error %v is not %v", err, want)
		}
	}
	_, err := p.Browse(ctx, BrowseParams{ID: "favorites"})
	check(err, ErrSignIn)
	_, err = p.Stream(ctx, StreamParams{Video: "v1", Part: "1", Quality: "1080"})
	check(err, ErrSetup)
	_, err = p.Video(ctx, VideoParams{ID: "gone"})
	check(err, ErrUnavailable)

	_, err = p.Browse(ctx, BrowseParams{ID: "nowhere"})
	var e *Error
	if !errors.As(err, &e) || e.Code != "" || e.Message != "No page nowhere" || !strings.Contains(e.Stack, "index.ts") {
		t.Fatalf("a plain error = %#v", err)
	}
	if errors.Is(err, ErrSignIn) || errors.Is(err, ErrSetup) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("a plain error %v has a code", err)
	}

	if err := p.SetSetting(ctx, "apiKey", "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(ctx, StreamParams{Video: "v1", Part: "1", Quality: "1080"}); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSetting(ctx, "quality", "4k"); err == nil {
		t.Fatal("a value none of the choices was stored")
	}
}

func TestDemoSignInDismissedAndBrowser(t *testing.T) {
	ctx := context.Background()
	ui := &fakeUI{dismiss: true}
	p := loadDemo(t, options(ui))
	if err := p.SetSetting(ctx, "browserSignIn", true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SignIn(ctx); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("signIn dismissed = %v", err)
	}
	if len(ui.opened) != 1 || ui.opened[0] != "https://demo.example.com/sign-in" {
		t.Fatalf("opened %v", ui.opened)
	}
}

func TestDemoTranslated(t *testing.T) {
	opts := options(&fakeUI{})
	opts.Language = "zh-CN"
	p := loadDemo(t, opts)
	if got := p.Translate("Popular"); got != "热门" {
		t.Fatalf("Translate = %q", got)
	}
	if got := p.Translate("Untranslated"); got != "Untranslated" {
		t.Fatalf("Translate of a text with no translation = %q", got)
	}
	home := must[*Page](t)(p.Home(context.Background(), PageParams{}))
	if home.Sections[0].Title != "热门" {
		t.Fatalf("home's first section = %q", home.Sections[0].Title)
	}

	// The app's own name of the language suits the plugin's locale.
	p.SetLanguage("zh-Hans")
	if got := p.Translate("Popular"); got != "热门" {
		t.Fatalf("Translate in zh-Hans = %q", got)
	}

	// Another language reaches the next call's runtime.
	p.SetLanguage("en")
	if got := p.Translate("Popular"); got != "Popular" {
		t.Fatalf("Translate in English = %q", got)
	}
	home = must[*Page](t)(p.Home(context.Background(), PageParams{}))
	if home.Sections[0].Title != "Popular" {
		t.Fatalf("home's first section in English = %q", home.Sections[0].Title)
	}
}

func TestStoppedPluginStartsAgain(t *testing.T) {
	p := loadDemo(t, options(&fakeUI{}))
	must[*Page](t)(p.Home(context.Background(), PageParams{}))
	p.Stop()
	must[*Page](t)(p.Home(context.Background(), PageParams{}))
	p.Close()
	if _, err := p.Home(context.Background(), PageParams{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("a call after Close = %v", err)
	}
}

func TestStorageAndSettingsPersist(t *testing.T) {
	ctx := context.Background()
	opts := options(&fakeUI{})
	opts.DataDir = t.TempDir()
	p := loadDemo(t, opts)
	must[*Account](t)(p.SignIn(ctx))
	if err := p.SetSetting(ctx, "quality", "480"); err != nil {
		t.Fatal(err)
	}
	p.Close()

	p = loadDemo(t, opts)
	if a := must[*Account](t)(p.Account(ctx)); a == nil {
		t.Fatal("the account was not kept")
	}
	if q := must[any](t)(p.Setting("quality")); q != "480" {
		t.Fatalf("quality = %v", q)
	}
}

// siteTransport answers every request it is sent with its host.
type siteTransport struct{}

func (siteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(req.URL.Hostname())), Header: http.Header{}, Request: req}, nil
}

const fetchSource = `
async function get(url) { return (await fetch(url)).text() }
export async function suggest({ query }) {
	try { return [await get(query)] } catch (e) { return ["denied: " + (e.cause?.message ?? e.message)] }
}`

func TestFetchReachesOnlyListedHosts(t *testing.T) {
	ctx := context.Background()
	opts := options(nil)
	opts.Client = &http.Client{Transport: siteTransport{}}
	p := inline(t, opts, `,"permissions":{"network":["api.example.com","*.cdn.example.com"]}`, fetchSource)
	for url, want := range map[string]string{
		"https://api.example.com/x":     "api.example.com",
		"https://img.cdn.example.com/x": "img.cdn.example.com",
		"https://example.com/x":         "denied",
		"https://evil.example.org/x":    "denied",
		"http://127.0.0.1/x":            "denied",
	} {
		got := must[[]string](t)(p.Suggest(ctx, SuggestParams{Query: url}))
		if !strings.HasPrefix(got[0], want) {
			t.Errorf("fetch %s = %q, want %q", url, got[0], want)
		}
	}

	none := inline(t, opts, ``, fetchSource)
	if got := must[[]string](t)(none.Suggest(ctx, SuggestParams{Query: "https://api.example.com/x"})); !strings.HasPrefix(got[0], "denied") {
		t.Errorf("fetch without the network permission = %q", got[0])
	}
}

func TestMemoryLimitStopsThePlugin(t *testing.T) {
	ctx := context.Background()
	opts := options(nil)
	opts.Limits.Memory = 8 << 20
	p := inline(t, opts, ``, `
const kept = [];
export function home({ cursor }) {
	if (cursor === "grow") for (;;) kept.push("x".repeat(1024) + kept.length);
	return { sections: [] };
}`)
	_, err := p.Home(ctx, PageParams{Cursor: "grow"})
	if !errors.Is(err, ErrMemory) {
		t.Fatalf("home past the memory limit = %v", err)
	}
	if _, err := p.Home(ctx, PageParams{}); err != nil {
		t.Fatalf("home after the runtime restarted = %v", err)
	}
}

func TestEndlessLoopIsInterrupted(t *testing.T) {
	ctx := context.Background()
	opts := options(nil)
	opts.Limits.Busy = 100 * time.Millisecond
	p := inline(t, opts, ``, `
export function home({ cursor }) {
	if (cursor === "spin") for (;;) {}
	return { sections: [] };
}
export async function browse() {
	await new Promise(r => setTimeout(r, 1));
	for (;;) {}
}
export function search() { return new Promise(r => setTimeout(() => r({ sections: [] }), 300)) }`)
	start := time.Now()
	if _, err := p.Home(ctx, PageParams{Cursor: "spin"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("an endless loop = %v", err)
	}
	if _, err := p.Browse(ctx, BrowseParams{ID: "x"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("an endless loop after a timer = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the endless loops took %v to stop", time.Since(start))
	}
	// Waiting is not running: a slow answer outlives the busy limit.
	if _, err := p.Search(ctx, SearchParams{Query: "x"}); err != nil {
		t.Fatalf("a slow answer = %v", err)
	}
}

func TestEndlessTopLevelIsInterrupted(t *testing.T) {
	opts := options(nil)
	opts.Limits.Busy = 100 * time.Millisecond
	p := inline(t, opts, ``, `for (;;) {} export function home() {}`)
	if _, err := p.Home(context.Background(), PageParams{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("an endless top level = %v", err)
	}
}

func TestCallTimesOut(t *testing.T) {
	opts := options(nil)
	opts.Limits.Call = 50 * time.Millisecond
	p := inline(t, opts, ``, `
export function home({ cursor }) { return cursor ? { sections: [] } : new Promise(() => {}) }`)
	if _, err := p.Home(context.Background(), PageParams{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a call that never answers = %v", err)
	}
	if _, err := p.Home(context.Background(), PageParams{Cursor: "x"}); err != nil {
		t.Fatalf("a call after one timed out = %v", err)
	}
}

func TestInvalidAnswersAreRefused(t *testing.T) {
	p := inline(t, options(nil), ``, `
export function home() { return { sections: [{ items: [{ kind: "video", title: "No id" }] }] } }
export function stream({ part }) {
  if (part === "both") return { video: { url: "https://cdn.example.com/v" }, subtitles: [{ label: "A", url: "https://cdn.example.com/a.vtt", text: "WEBVTT" }] }
  return { video: { url: part ? "https://elsewhere.example.com/v" : "file:///etc/passwd" } }
}
export function video({ id }) {
  if (id === "show") return { id, title: "T", parts: [{ id: "1", title: "P1" }], show: { id: "s", status: "watching" } }
  return id ? { id, title: "T", parts: [{ id: "1", title: "P1" }], part: "2" } : "not a video"
}
export function following() { return { shows: [{ id: "s", title: "S", video: "v", status: "dropped" }] } }
export function danmaku({ part }) { return part ? [{ time: 1, text: "a", mode: "side" }] : [{ time: -1, text: "", color: "red" }] }`)
	ctx := context.Background()
	var inv *InvalidError
	if _, err := p.Home(ctx, PageParams{}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "id is empty") {
		t.Fatalf("home without an id = %v", err)
	}
	if _, err := p.Stream(ctx, StreamParams{}); !errors.As(err, &inv) {
		t.Fatalf("stream of a file URL = %v", err)
	}
	if _, err := p.Stream(ctx, StreamParams{Part: "1"}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "network permission") {
		t.Fatalf("stream of an unlisted host = %v", err)
	}
	if _, err := p.Stream(ctx, StreamParams{Part: "both"}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "both a url and text") {
		t.Fatalf("subtitles of a URL and text = %v", err)
	}
	if _, err := p.Video(ctx, VideoParams{}); !errors.As(err, &inv) {
		t.Fatalf("video of a string = %v", err)
	}
	if _, err := p.Video(ctx, VideoParams{ID: "v"}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "not among its parts") {
		t.Fatalf("video starting at a part it lacks = %v", err)
	}
	if _, err := p.Following(ctx, FollowingParams{}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "none of the manifest's statuses") {
		t.Fatalf("following with a status the manifest lacks = %v", err)
	}
	if _, err := p.Video(ctx, VideoParams{ID: "show"}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "none of the manifest's statuses") {
		t.Fatalf("video of a show with a status the manifest lacks = %v", err)
	}
	if _, err := p.Danmaku(ctx, DanmakuParams{}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "time is negative") || !strings.Contains(err.Error(), "text is empty") || !strings.Contains(err.Error(), "not a color") {
		t.Fatalf("danmaku without text, at a negative time, of a named color = %v", err)
	}
	if _, err := p.Danmaku(ctx, DanmakuParams{Part: "1"}); !errors.As(err, &inv) || !strings.Contains(err.Error(), "none of scroll") {
		t.Fatalf("danmaku of an unknown mode = %v", err)
	}
	if _, err := p.Search(ctx, SearchParams{}); !errors.Is(err, ErrNotExported) || p.Has(ExportSearch) {
		t.Fatalf("search, not exported = %v", err)
	}
}

func TestSandbox(t *testing.T) {
	p := inline(t, options(nil), ``, `
export function suggest() {
	const out = [];
	for (const f of [() => eval("1"), () => new Function("return 1")(), () => host.openURL("https://example.com")])
		try { f(); out.push("ran") } catch (e) { out.push(e.name) }
	out.push(typeof require, typeof process, typeof URL, typeof setTimeout, host.md5("abc"));
	return out;
}`)
	got := strings.Join(must[[]string](t)(p.Suggest(context.Background(), SuggestParams{})), " ")
	if want := "EvalError EvalError Error undefined undefined function function 900150983cd24fb0d6963f7d28e17f72"; got != want {
		t.Fatalf("sandbox = %s, want %s", got, want)
	}
}

func TestManifests(t *testing.T) {
	base := `"id":"test","name":"Test","version":"1.0.0","main":"main.js","media":"video"`
	if _, err := ParseManifest([]byte(`{`+base+`,"minAppVersion":"0.1.0"}`), "0.1.0"); err != nil {
		t.Fatal(err)
	}
	block := "id: test\nname: Test\nversion: 1.0.0\nminAppVersion: 0.1.0\nmain: main.js\nmedia: video\npermissions:\n  network: [api.example.com]\nsettings:\n  - {key: hd, label: HD, type: toggle, default: true}\n"
	if m, err := ParseManifest([]byte(block), "0.1.0"); err != nil || m.Permissions.Network[0] != "api.example.com" || m.Settings[0].Default != true {
		t.Fatalf("a block manifest = %+v, %v", m, err)
	}
	_, err := ParseManifest([]byte(`{`+base+`,"minAppVersion":"0.2.0","future":true}`), "0.1.0")
	var tooNew *TooNewError
	if !errors.As(err, &tooNew) || !strings.Contains(err.Error(), "update the app") {
		t.Fatalf("a too-new minAppVersion = %v", err)
	}
	for _, bad := range []string{
		`{` + base + `,"minAppVersion":"0.1.0","future":true}`,
		`{"id":"Bad Id","name":"Test","version":"1.0.0","minAppVersion":"0.1.0","main":"main.js","media":"video"}`,
		`{"id":"test","name":"Test","version":"1.0","minAppVersion":"0.1.0","main":"main.js","media":"video"}`,
		`{"id":"test","name":"Test","version":"1.0.0","minAppVersion":"0.1.0","main":"../main.js","media":"video"}`,
		`{"id":"test","name":"Test","version":"1.0.0","minAppVersion":"0.1.0","main":"main.js","media":"image"}`,
		`{` + base + `,"minAppVersion":"0.1.0","permissions":{"network":["*"]}}`,
		`{` + base + `,"minAppVersion":"0.1.0","permissions":{"network":["example.com:8080"]}}`,
		`{` + base + `,"minAppVersion":"0.1.0","settings":[{"key":"q","label":"Q","type":"choice","default":"x","choices":[{"value":"y","label":"Y"}]}]}`,
		`{` + base + `,"minAppVersion":"0.1.0","settings":[{"key":"t","label":"T","type":"toggle","default":"yes"}]}`,
		`{` + base + `,"minAppVersion":"0.1.0","navigation":[{"id":"a","label":"A"},{"id":"a","label":"B"}]}`,
		`{` + base + `,"minAppVersion":"0.1.0","statuses":[{"id":"w","label":"W"},{"id":"w","label":"X"}]}`,
		`{` + base + `,"minAppVersion":"0.1.0","statuses":[{"id":"w","label":""}]}`,
		`{` + base + `,"minAppVersion":"0.1.0","icon":"icon.png"}`,
		`{` + base + `,"minAppVersion":"0.1.0","icon":"../icon.svg"}`,
		`{` + base + `,"minAppVersion":"0.1.0","color":"orange"}`,
	} {
		if _, err := ParseManifest([]byte(bad), "0.1.0"); err == nil {
			t.Errorf("manifest %s was accepted", bad)
		}
	}
}

func TestFollowNeedsStatuses(t *testing.T) {
	fsys := fstest.MapFS{
		"plugin.yaml": {Data: []byte("id: test\nname: Test\nversion: 1.0.0\nminAppVersion: 0.1.0\nmain: main.js\nmedia: video\n")},
		"main.js":     {Data: []byte("export function follow() {}")},
	}
	if _, err := Load(fsys, false, options(nil)); err == nil || !strings.Contains(err.Error(), "lists no statuses") {
		t.Fatalf("follow without statuses = %v", err)
	}
}

func TestNativePlugin(t *testing.T) {
	manifest := func(extra string) fstest.MapFS {
		return fstest.MapFS{
			"plugin.yaml":        {Data: []byte("id: native\nname: Files\nversion: 1.0.0\nminAppVersion: 0.1.0\nmedia: video\n" + extra)},
			"locales/zh-CN.yaml": {Data: []byte("Files: 文件\n")},
		}
	}
	exports := Native{
		ExportStream: func(_ context.Context, _ *Plugin, raw json.RawMessage) (any, error) {
			var params StreamParams
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, err
			}
			if params.Part == "gone" {
				return nil, &Error{Code: CodeUnavailable, Message: "gone"}
			}
			return Stream{Video: &Source{URL: FileURL("/videos/a.mkv")}, Subtitles: []Subtitle{{Label: "A", Source: Source{URL: FileURL("/videos/a.srt")}}}}, nil
		},
		ExportResolve: func(context.Context, *Plugin, json.RawMessage) (any, error) {
			return Item{Kind: KindVideo, ID: "a", Title: "A", Image: &Source{URL: FileURL("/videos/a.jpg")}}, nil
		},
	}
	p, err := LoadNative(manifest(""), exports, Options{AppVersion: "0.1.0", Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx := context.Background()
	if !p.Builtin || !p.Has(ExportStream) || p.Has(ExportHome) || p.Name() != "文件" {
		t.Fatalf("plugin %+v named %q", p.Manifest, p.Name())
	}
	s, err := p.Stream(ctx, StreamParams{Video: "a", Part: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if path, ok := FilePath(s.Video.URL); !ok || filepath.ToSlash(path) != "/videos/a.mkv" && filepath.ToSlash(path) != "videos/a.mkv" {
		t.Fatalf("stream %q", s.Video.URL)
	}
	var e *Error
	if _, err := p.Stream(ctx, StreamParams{Part: "gone"}); !errors.Is(err, ErrUnavailable) || !errors.As(err, &e) || e.Plugin != "native" {
		t.Fatalf("a thrown error = %v", err)
	}
	var inv *InvalidError
	if _, err := p.Resolve(ctx, ResolveParams{}); !errors.As(err, &inv) {
		t.Fatalf("a picture of a file = %v", err)
	}
	if _, err := p.Home(ctx, PageParams{}); !errors.Is(err, ErrNotExported) {
		t.Fatalf("home, not exported = %v", err)
	}
	if _, err := LoadNative(manifest("main: main.js\n"), exports, Options{AppVersion: "0.1.0"}); err == nil {
		t.Fatal("a native plugin with a main module loaded")
	}
	if _, err := Load(manifest(""), true, Options{AppVersion: "0.1.0"}); err == nil {
		t.Fatal("a module plugin without a main module loaded")
	}
	if _, err := LoadNative(manifest(""), Native{"play": exports[ExportStream]}, Options{AppVersion: "0.1.0"}); err == nil {
		t.Fatal("a native export outside the contract loaded")
	}
	p.Close()
	if _, err := p.Stream(ctx, StreamParams{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("a call after Close = %v", err)
	}
}
