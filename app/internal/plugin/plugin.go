package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MoeForge/moejs"
)

// do runs export with params and decodes its answer into out, which check
// validates; a nil out discards the answer.
func (p *Plugin) do(ctx context.Context, export string, params any, out any, check func(*checker)) error {
	if !p.exports[export] {
		return fmt.Errorf("plugin %s: %s: %w", p.Manifest.ID, export, ErrNotExported)
	}
	timeout := p.opts.Limits.Call
	if export == ExportSignIn {
		timeout = p.opts.Limits.SignIn
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := p.call(ctx, export, params)
	if err != nil || out == nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &InvalidError{Plugin: p.Manifest.ID, Export: export, Err: err}
	}
	if check != nil {
		c := checker{allow: p.allow, files: p.native != nil, statuses: p.Manifest.Statuses}
		check(&c)
		if err := c.err(); err != nil {
			return &InvalidError{Plugin: p.Manifest.ID, Export: export, Err: err}
		}
	}
	return nil
}

// call runs export, natively or in the runtime, and returns the JSON of
// its answer.
func (p *Plugin) call(ctx context.Context, export string, params any) ([]byte, error) {
	if p.native != nil {
		return p.callNative(ctx, export, params)
	}
	r, err := p.runtime()
	if err != nil {
		return nil, err
	}
	if export == ExportSignIn {
		defer r.loop.RunOnLoop(func(*moejs.Runtime) { r.closePrompt() })
	}
	return r.call(ctx, export, params)
}

// Account is the signed-in account, or nil.
func (p *Plugin) Account(ctx context.Context) (*Account, error) {
	var a *Account
	err := p.do(ctx, ExportAccount, struct{}{}, &a, func(c *checker) {
		if a != nil {
			a.check(c)
		}
	})
	return a, err
}

// SignIn signs the viewer in, through the plugin's prompt, and returns
// the account.
func (p *Plugin) SignIn(ctx context.Context) (*Account, error) {
	var a *Account
	err := p.do(ctx, ExportSignIn, struct{}{}, &a, func(c *checker) {
		if a == nil {
			c.fail("account", "is null")
			return
		}
		a.check(c)
	})
	return a, err
}

// SignOut signs the viewer out.
func (p *Plugin) SignOut(ctx context.Context) error {
	return p.do(ctx, ExportSignOut, struct{}{}, nil, nil)
}

func (p *Plugin) page(ctx context.Context, export string, params any) (*Page, error) {
	var pg Page
	if err := p.do(ctx, export, params, &pg, pg.check); err != nil {
		return nil, err
	}
	return &pg, nil
}

// Home is the plugin's own page.
func (p *Plugin) Home(ctx context.Context, params PageParams) (*Page, error) {
	return p.page(ctx, ExportHome, params)
}

// Browse opens the page of an id: a sidebar entry's, an uploader's, a
// collection's or a link's.
func (p *Plugin) Browse(ctx context.Context, params BrowseParams) (*Page, error) {
	return p.page(ctx, ExportBrowse, params)
}

// Search is the results of a query, in sections by kind.
func (p *Plugin) Search(ctx context.Context, params SearchParams) (*Page, error) {
	return p.page(ctx, ExportSearch, params)
}

// Suggest completes a query.
func (p *Plugin) Suggest(ctx context.Context, params SuggestParams) ([]string, error) {
	var s []string
	err := p.do(ctx, ExportSuggest, params, &s, nil)
	return s, err
}

// Resolve turns a pasted link or ID into the item it names, or nil.
func (p *Plugin) Resolve(ctx context.Context, params ResolveParams) (*Item, error) {
	var it *Item
	err := p.do(ctx, ExportResolve, params, &it, func(c *checker) {
		if it != nil {
			it.check(c)
		}
	})
	return it, err
}

// Video is a video's details, parts and related videos.
func (p *Plugin) Video(ctx context.Context, params VideoParams) (*Video, error) {
	var v Video
	if err := p.do(ctx, ExportVideo, params, &v, v.check); err != nil {
		return nil, err
	}
	return &v, nil
}

// Stream is what to play for a part at a quality.
func (p *Plugin) Stream(ctx context.Context, params StreamParams) (*Stream, error) {
	var s Stream
	if err := p.do(ctx, ExportStream, params, &s, s.check); err != nil {
		return nil, err
	}
	return &s, nil
}

// Progress reports the position the viewer reached.
func (p *Plugin) Progress(ctx context.Context, params ProgressParams) error {
	return p.do(ctx, ExportProgress, params, nil, nil)
}

// Following is the shows the account follows, newest update first.
func (p *Plugin) Following(ctx context.Context, params FollowingParams) (*Following, error) {
	var f Following
	if err := p.do(ctx, ExportFollowing, params, &f, f.check); err != nil {
		return nil, err
	}
	return &f, nil
}

// Follow follows a show on the account, or changes its status.
func (p *Plugin) Follow(ctx context.Context, params FollowParams) error {
	if _, ok := p.Manifest.Status(params.Status); !ok {
		return fmt.Errorf("plugin %s has no status %q", p.Manifest.ID, params.Status)
	}
	return p.do(ctx, ExportFollow, params, nil, nil)
}

// Unfollow unfollows a show on the account.
func (p *Plugin) Unfollow(ctx context.Context, params UnfollowParams) error {
	return p.do(ctx, ExportUnfollow, params, nil, nil)
}

// Danmaku is the comments of a span of a part, in no particular order.
func (p *Plugin) Danmaku(ctx context.Context, params DanmakuParams) ([]Danmaku, error) {
	var list []Danmaku
	err := p.do(ctx, ExportDanmaku, params, &list, func(c *checker) { checkDanmaku(list, c) })
	return list, err
}

// Setting is the viewer's value of a setting, or its default.
func (p *Plugin) Setting(key string) (any, error) {
	s, ok := p.Manifest.Setting(key)
	if !ok {
		return nil, fmt.Errorf("plugin %s has no setting %q", p.Manifest.ID, key)
	}
	return s.value(p.data.setting(key)), nil
}

// SetSetting stores the viewer's value of a setting and tells the plugin,
// when it exports setSetting.
func (p *Plugin) SetSetting(ctx context.Context, key string, value any) error {
	s, ok := p.Manifest.Setting(key)
	if !ok {
		return fmt.Errorf("plugin %s has no setting %q", p.Manifest.ID, key)
	}
	if err := s.check(value, false); err != nil {
		return fmt.Errorf("plugin %s: setting %q %w", p.Manifest.ID, key, err)
	}
	if err := p.data.setSetting(key, value); err != nil {
		return err
	}
	if !p.Has(ExportSetSetting) {
		return nil
	}
	return p.do(ctx, ExportSetSetting, SettingParams{Key: key, Value: value}, nil, nil)
}
