package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
)

// Native is a built-in plugin's exports written in Go, by name, for what
// no sandbox may reach, such as the viewer's own files. Each takes the
// JSON a module's export would and answers a value of the contract's
// types or an *Error. Its answers pass the same checks as a module's,
// except that its streams and subtitles may be file URLs.
type Native map[string]func(ctx context.Context, p *Plugin, params json.RawMessage) (any, error)

// LoadNative loads a built-in plugin of native exports, whose folder
// holds its manifest, naming no main module, and its locales.
func LoadNative(fsys fs.FS, exports Native, opts Options) (*Plugin, error) {
	m, err := readManifest(fsys, opts)
	if err != nil {
		return nil, err
	}
	if m.Main != "" {
		return nil, fmt.Errorf("plugin %s: a native plugin has no main module", m.ID)
	}
	p, err := newPlugin(fsys, m, true, opts)
	if err != nil {
		return nil, err
	}
	p.native = exports
	for name := range exports {
		if !slices.Contains(Exports, name) {
			return nil, fmt.Errorf("plugin %s: %s is not an export of the contract", m.ID, name)
		}
		p.exports[name] = true
	}
	return p, nil
}

// callNative runs a native export and returns the JSON of its answer.
func (p *Plugin) callNative(ctx context.Context, export string, params any) ([]byte, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	arg, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	v, err := p.native[export](ctx, p, arg)
	if err != nil {
		if e, ok := err.(*Error); ok && e.Plugin == "" {
			e.Plugin = p.Manifest.ID
		}
		return nil, err
	}
	return json.Marshal(v)
}

// FileURL is the file URL of a local path, as a native plugin answers it.
func FileURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // a Windows drive
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// FilePath is the local path of a file URL, and whether raw is one.
func FilePath(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
		return "", false
	}
	path := u.Path
	if goruntime.GOOS == "windows" {
		path = strings.TrimPrefix(path, "/")
	}
	return filepath.FromSlash(path), true
}
