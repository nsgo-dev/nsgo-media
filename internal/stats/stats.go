// Package stats counts the use of the demo and NSGo Player in the
// website's Umami, as pages of the site: the site's own pages are tagged
// "website", the demo's "demo" and the app's "app". An install carries a
// random ID kept in the storage, so that one install is one visitor.
// Builds of nsgo dev and test binaries report nothing, nor an app whose
// viewer turned it off (Options.On).
package stats

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

const (
	sendURL  = "https://umami.oneo.me/api/send"
	website  = "4cb99b9f-3529-4c88-9507-3607baaf15b3"
	hostname = "media.nsgo.dev"
	idKey    = "statsInstallID"
)

// Umami measures a visit from its first event to its last, so while the
// app is used it sends a heartbeat each beat: until idle after the last
// page shown, or while Busy says so.
const (
	beat = time.Minute
	idle = 5 * time.Minute
)

// Options are what an app reports.
type Options struct {
	// Tag tells the app apart, as "demo" or "app".
	Tag string
	// Page returns the page shown, its path under the site and its title;
	// a pageview is sent each time it changes. It is read in an effect.
	Page func() (path, title string)
	// On reports whether the viewer leaves the statistics on, checked
	// before each event; nil is always.
	On func() bool
	// Busy reports whether the app is in use though no page changed, as
	// while a video plays; nil is never.
	Busy func() bool
}

// payload is an event of Umami's /api/send.
type payload struct {
	Website  string `json:"website"`
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Language string `json:"language,omitempty"`
	Screen   string `json:"screen,omitempty"`
	Tag      string `json:"tag"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
}

// Report sends a pageview whenever the page changes, and the heartbeats
// while the app is in use. Events are made on the graph's goroutine and
// sent from another, oldest first; one that fails is dropped.
func Report(cx *core.Scope, o Options) {
	if !enabled || testing.Testing() {
		return
	}
	env := cx.Env()
	id := installID(cx)
	queue := make(chan payload, 32)
	go send(queue, agent(env.OS()))

	var path, title string
	var since time.Time
	event := func(name string) {
		if o.On != nil && !o.On() {
			return
		}
		w := env.Window()
		p := payload{Website: website, Hostname: hostname, URL: path, Title: title,
			Language: env.Locale(), Screen: fmt.Sprintf("%dx%d", int(w.Width), int(w.Height)),
			Tag: o.Tag, ID: id, Name: name}
		select {
		case queue <- p:
		default:
		}
	}
	cx.Effect(func() {
		p, t := o.Page()
		core.Untracked(cx, func() bool {
			if p != path || since.IsZero() {
				path, title, since = p, t, time.Now()
				event("")
			}
			return true
		})
	})
	var tick func()
	tick = func() {
		if time.Since(since) < idle || (o.Busy != nil && core.Untracked(cx, o.Busy)) {
			event("heartbeat")
		}
		cx.AfterFunc(beat, tick)
	}
	cx.AfterFunc(beat, tick)
}

// installID returns the install's random ID, made on first use.
func installID(cx *core.Scope) string {
	store := cx.Storage()
	id, ok := store.GetItem(idKey)
	if !ok {
		b := make([]byte, 16)
		rand.Read(b)
		id = hex.EncodeToString(b)
		store.SetItem(idKey, id)
	}
	return id
}

// send posts the queue's events to Umami until it closes.
func send(queue <-chan payload, agent string) {
	client := &http.Client{Timeout: 10 * time.Second}
	for p := range queue {
		body, _ := json.Marshal(map[string]any{"type": "event", "payload": p})
		req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if agent != "" {
			req.Header.Set("User-Agent", agent)
		}
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	}
}

// agent is the User-Agent sent, as a browser on the platform would say
// it, for Umami to tell the system and not take it for a bot as it does
// Go's own.
func agent(os core.OS) string {
	platforms := map[core.OS]string{
		core.OSMacOS:   "Macintosh; Intel Mac OS X 10_15_7",
		core.OSWindows: "Windows NT 10.0; Win64; x64",
		core.OSLinux:   "X11; Linux x86_64",
		core.OSIOS:     "iPhone; CPU iPhone OS 18_0 like Mac OS X",
		core.OSAndroid: "Linux; Android 15",
	}
	p, ok := platforms[os]
	if !ok {
		return ""
	}
	return "Mozilla/5.0 (" + p + ") NSGoMedia/1.0"
}
