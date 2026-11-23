package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
)

const (
	// maxPicture bounds a downloaded picture, and keptPictures how many
	// the app keeps in memory.
	maxPicture   = 8 << 20
	keptPictures = 300
)

// pictures downloads plugins' pictures with the headers they answered,
// which the platform's image views cannot send, keeping the latest.
type pictures struct {
	client *http.Client

	mu    sync.Mutex
	kept  map[string][]byte
	order []string
}

func newPictures(client *http.Client) *pictures {
	return &pictures{client: client, kept: map[string][]byte{}}
}

func pictureKey(s plugin.Source) string {
	keys := make([]string, 0, len(s.Headers))
	for k := range s.Headers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString(s.URL)
	for _, k := range keys {
		b.WriteString("\n" + k + ": " + s.Headers[k])
	}
	return b.String()
}

// get is the picture of s, downloaded unless it is kept.
func (ps *pictures) get(ctx context.Context, s plugin.Source) ([]byte, error) {
	key := pictureKey(s)
	ps.mu.Lock()
	data, ok := ps.kept[key]
	ps.mu.Unlock()
	if ok {
		return data, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	resp, err := ps.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("picture %s: %s", s.URL, resp.Status)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxPicture+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPicture {
		return nil, fmt.Errorf("picture %s is larger than %d MB", s.URL, maxPicture>>20)
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, ok := ps.kept[key]; !ok {
		ps.kept[key] = data
		ps.order = append(ps.order, key)
		if len(ps.order) > keptPictures {
			delete(ps.kept, ps.order[0])
			ps.order = ps.order[1:]
		}
	}
	return data, nil
}

// picture shows a plugin's picture in a well of ratio, its placeholder
// while it downloads, when it fails and when there is none.
func (sh *shell) picture(cx *core.Scope, src *plugin.Source, ratio float32, style core.Style) core.Node {
	data := core.NewSignalFunc[[]byte](cx, nil, nil)
	if src != nil {
		call(cx, cx.Context(), func(ctx context.Context) ([]byte, error) { return sh.pictures.get(ctx, *src) }, func(b []byte, err error) {
			if err == nil {
				data.Set(b)
			}
		})
	}
	return core.Dynamic(func(cx *core.Scope) core.Node {
		return ui.Image{Source: core.ImageSource{Data: data.Get()}, AspectRatio: ratio, Style: style}
	})
}
