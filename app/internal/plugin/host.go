package plugin

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/MoeForge/moejs"
)

// UI is what the app shows for plugins. Its methods are called on a
// plugin's runtime goroutine and must not block it.
type UI interface {
	// ShowSignIn shows p's sign-in prompt, replacing one it shows.
	ShowSignIn(p *Plugin, prompt SignInPrompt)
	// CloseSignIn closes p's sign-in prompt, if one is shown.
	CloseSignIn(p *Plugin)
	// OpenURL opens an http or https URL in the browser.
	OpenURL(url string) error
}

// SignInPrompt is what a plugin asks the viewer to do to sign in.
type SignInPrompt struct {
	// QRCode is the text of a QR code to scan; "" shows none.
	QRCode  string
	Message string
	// Dismissed tells the plugin the viewer closed the prompt. It may be
	// called from any goroutine.
	Dismissed func()
}

// prompt is the sign-in prompt a runtime shows, whose promise settles to
// whether the viewer dismissed it.
type prompt struct{ settle func(dismissed bool) }

type native = func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error)

// install sets the globals a plugin has beyond the standard library and
// the Web APIs: console and host.
func (r *runtime) install() error {
	log := func(level slog.Level) native {
		return func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = r.text(a)
			}
			r.p.opts.Logger.Log(context.Background(), level, strings.Join(parts, " "))
			return moejs.Undefined(), nil
		}
	}
	console := map[string]any{
		"log":   moejs.NativeFunc(log(slog.LevelInfo)),
		"info":  moejs.NativeFunc(log(slog.LevelInfo)),
		"debug": moejs.NativeFunc(log(slog.LevelDebug)),
		"warn":  moejs.NativeFunc(log(slog.LevelWarn)),
		"error": moejs.NativeFunc(log(slog.LevelError)),
	}
	if err := r.rt.SetGlobal("console", console); err != nil {
		return err
	}
	host := map[string]any{
		"language": r.p.opts.Language,
		"t":        moejs.NativeFunc(r.translate),
		"md5":      moejs.NativeFunc(r.md5),
		"storage": map[string]any{
			"get":    moejs.NativeFunc(r.storageGet),
			"set":    moejs.NativeFunc(r.storageSet),
			"remove": moejs.NativeFunc(r.storageRemove),
		},
		"settings": map[string]any{
			"get": moejs.NativeFunc(r.settingGet),
		},
		"showSignIn":  moejs.NativeFunc(r.showSignIn),
		"closeSignIn": moejs.NativeFunc(r.closeSignIn),
		"openURL":     moejs.NativeFunc(r.openURL),
	}
	return r.rt.SetGlobal("host", host)
}

// str reads argument i as a string.
func str(args []moejs.Value, i int, name string) (string, error) {
	v := moejs.Arg(args, i)
	if !v.IsString() {
		return "", fmt.Errorf("%s is not a string", name)
	}
	return v.AsString().GoString(), nil
}

func (r *runtime) translate(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	text, err := str(args, 0, "host.t's text")
	if err != nil {
		return moejs.Undefined(), err
	}
	return moejs.String(r.p.Translate(text)), nil
}

func (r *runtime) md5(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	text, err := str(args, 0, "host.md5's text")
	if err != nil {
		return moejs.Undefined(), err
	}
	sum := md5.Sum([]byte(text))
	return moejs.String(hex.EncodeToString(sum[:])), nil
}

func (r *runtime) storageGet(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	key, err := str(args, 0, "host.storage.get's key")
	if err != nil {
		return moejs.Undefined(), err
	}
	raw, ok := r.p.data.get(key)
	if !ok {
		return moejs.Undefined(), nil
	}
	return r.rt.ParseJSON(raw)
}

func (r *runtime) storageSet(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	key, err := str(args, 0, "host.storage.set's key")
	if err != nil {
		return moejs.Undefined(), err
	}
	v := moejs.Arg(args, 1)
	var raw json.RawMessage
	if !v.IsUndefined() {
		if raw, err = r.rt.AppendJSON(nil, v); err != nil {
			return moejs.Undefined(), err
		}
	}
	return moejs.Undefined(), r.p.data.set(key, raw)
}

func (r *runtime) storageRemove(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	key, err := str(args, 0, "host.storage.remove's key")
	if err != nil {
		return moejs.Undefined(), err
	}
	return moejs.Undefined(), r.p.data.set(key, nil)
}

func (r *runtime) settingGet(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	key, err := str(args, 0, "host.settings.get's key")
	if err != nil {
		return moejs.Undefined(), err
	}
	s, ok := r.p.Manifest.Setting(key)
	if !ok {
		return moejs.Undefined(), fmt.Errorf("the manifest has no setting %q", key)
	}
	return r.rt.FromGo(s.value(r.p.data.setting(key)))
}

func (r *runtime) showSignIn(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	ui := r.p.opts.UI
	if ui == nil {
		return moejs.Undefined(), errors.New("this app cannot show a sign-in prompt")
	}
	v := moejs.Arg(args, 0)
	if !v.IsObject() {
		return moejs.Undefined(), errors.New("host.showSignIn takes { qrCode, message }")
	}
	sp := SignInPrompt{QRCode: r.prop(v, "qrCode"), Message: r.prop(v, "message")}
	if sp.QRCode == "" && sp.Message == "" {
		return moejs.Undefined(), errors.New("host.showSignIn needs a qrCode or a message")
	}
	p, settle := r.loop.NewPromise()
	cur := &prompt{settle: func(dismissed bool) {
		settle(func(*moejs.Runtime) (moejs.Value, error) { return moejs.Bool(dismissed), nil })
	}}
	if r.prompt != nil {
		r.prompt.settle(false)
	}
	r.prompt = cur
	sp.Dismissed = func() { cur.settle(true) }
	ui.ShowSignIn(r.p, sp)
	return p, nil
}

func (r *runtime) closeSignIn(_ *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
	r.closePrompt()
	return moejs.Undefined(), nil
}

// closePrompt closes the sign-in prompt the runtime shows; it runs on the
// loop.
func (r *runtime) closePrompt() {
	if r.prompt == nil {
		return
	}
	r.prompt.settle(false)
	r.prompt = nil
	r.p.opts.UI.CloseSignIn(r.p)
}

func (r *runtime) openURL(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	s, err := str(args, 0, "host.openURL's url")
	if err != nil {
		return moejs.Undefined(), err
	}
	if u, err := url.Parse(s); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return moejs.Undefined(), fmt.Errorf("host.openURL opens only http and https URLs, not %q", s)
	}
	if r.p.opts.UI == nil {
		return moejs.Undefined(), errors.New("this app cannot open URLs")
	}
	return moejs.Undefined(), r.p.opts.UI.OpenURL(s)
}
