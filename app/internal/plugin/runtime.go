package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/eventloop"
	"github.com/MoeForge/moejs/webapi"
)

var (
	// ErrBusy stops a runtime that ran JavaScript for longer than
	// Limits.Busy without waiting.
	ErrBusy = errors.New("plugin: ran too long without waiting")
	// ErrMemory stops a runtime that passed Limits.Memory.
	ErrMemory = errors.New("plugin: out of memory")
	// ErrClosed is a call to a closed plugin.
	ErrClosed = errors.New("plugin: closed")
	// ErrNotExported is a call to an export the plugin does not have.
	ErrNotExported = errors.New("plugin: not exported")
)

// settleScript passes a call's value, or what its promise settles to, to
// one of two host functions.
var settleScript = func() *moejs.Script {
	s, err := moejs.CompileScript("settle.js", "((v, ok, fail) => { Promise.resolve(v).then(ok, fail) })")
	if err != nil {
		panic(err)
	}
	return s
}()

// runtime is one moejs runtime running a plugin on an event loop of its
// own goroutine. A limit or Close ends it, failing the calls in flight.
type runtime struct {
	p      *Plugin
	rt     *moejs.Runtime
	loop   *eventloop.Loop
	api    *webapi.API
	settle moejs.Value
	busy   *time.Timer
	prompt *prompt

	mu    sync.Mutex
	calls map[chan result]bool
	ended bool
	err   error
	done  chan struct{}
}

type result struct {
	data []byte
	err  error
}

// runtime returns the plugin's runtime, starting one when it has none.
func (p *Plugin) runtime() (*runtime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	if p.rt != nil {
		return p.rt, nil
	}
	r, err := p.start()
	if err != nil {
		return nil, err
	}
	p.rt = r
	return r, nil
}

func (p *Plugin) start() (*runtime, error) {
	lim := p.opts.Limits
	rt := moejs.NewRuntime(moejs.Options{
		MemoryLimit:        lim.Memory,
		DisableDynamicCode: true,
		Locale:             p.opts.Language,
	})
	r := &runtime{p: p, rt: rt, calls: map[chan result]bool{}, done: make(chan struct{})}
	loop, err := eventloop.New(rt, eventloop.Options{
		OnError: func(err error) { p.opts.Logger.Warn("uncaught", "error", r.describe(err)) },
		OnIdle:  func() { r.busy.Stop() },
		OnWake:  func() error { r.busy.Reset(lim.Busy); return nil },
	})
	if err != nil {
		return nil, err
	}
	r.loop = loop
	// The watchdog runs while JavaScript does: through the module's top
	// level, and on the loop from each wake to the next idle.
	r.busy = time.AfterFunc(lim.Busy, func() { loop.Interrupt(ErrBusy) })
	opts := webapi.Options{}
	if hosts := p.Manifest.Permissions.Network; len(hosts) > 0 {
		opts.Client = p.opts.Client
		if opts.Client == nil {
			opts.Client = &http.Client{Timeout: 30 * time.Second}
		}
		opts.Allow = p.allow
	}
	if r.api, err = webapi.Install(loop, opts); err == nil {
		err = r.install()
	}
	if err == nil {
		r.settle, err = rt.RunScript(settleScript)
	}
	if err == nil {
		err = rt.Load(p.mod)
		if errors.Is(err, moejs.ErrModulePending) {
			err = nil
		}
	}
	if err != nil {
		r.busy.Stop()
		if r.api != nil {
			r.api.Close()
		}
		return nil, r.describe(err)
	}
	r.busy.Reset(lim.Busy)
	ready := make(chan struct{})
	go func() {
		err := loop.Run(func(*moejs.Runtime) error {
			loop.Hold() // runs until a limit or Close ends it
			close(ready)
			return nil
		})
		r.end(err)
	}()
	<-ready
	return r, nil
}

// stopped tells a limit's interrupt from other errors.
func (r *runtime) stopped(err error) error {
	if errors.Is(err, moejs.ErrMemoryLimit) {
		return fmt.Errorf("%w: %w", ErrMemory, err)
	}
	return err
}

// end fails the calls in flight once the loop ended.
func (r *runtime) end(err error) {
	r.busy.Stop()
	r.api.Close()
	if errors.Is(err, eventloop.ErrTerminated) {
		err = ErrClosed
	}
	err = fmt.Errorf("plugin %s stopped: %w", r.p.Manifest.ID, r.stopped(err))
	r.mu.Lock()
	r.ended, r.err = true, err
	for ch := range r.calls {
		ch <- result{err: err}
	}
	clear(r.calls)
	r.mu.Unlock()
	close(r.done)
	r.p.mu.Lock()
	if r.p.rt == r {
		r.p.rt = nil
	}
	r.p.mu.Unlock()
	if r.prompt != nil {
		r.p.opts.UI.CloseSignIn(r.p)
	}
	if !errors.Is(err, ErrClosed) {
		r.p.opts.Logger.Warn("stopped", "error", err)
	}
}

// stop ends the runtime and waits for it.
func (r *runtime) stop() {
	r.loop.Terminate()
	<-r.done
}

// deliver hands a call its result unless it gave up or the runtime ended.
func (r *runtime) deliver(ch chan result, res result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls[ch] {
		delete(r.calls, ch)
		ch <- res
	}
}

// call runs export with params and returns the JSON of what it answered.
func (r *runtime) call(ctx context.Context, export string, params any) ([]byte, error) {
	arg, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	hook, err := r.p.mod.Hook(export)
	if err != nil {
		return nil, err
	}
	ch := make(chan result, 1)
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return nil, r.err
	}
	r.calls[ch] = true
	r.mu.Unlock()
	r.loop.RunOnLoop(func(rt *moejs.Runtime) { r.invoke(ch, hook, arg) })
	select {
	case res := <-ch:
		return res.data, res.err
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.calls, ch)
		r.mu.Unlock()
		select {
		case res := <-ch:
			return res.data, res.err
		default:
		}
		return nil, fmt.Errorf("plugin %s: %s: %w", r.p.Manifest.ID, export, context.Cause(ctx))
	}
}

// invoke calls the export on the loop and delivers what it settles to.
func (r *runtime) invoke(ch chan result, hook moejs.Hook, arg []byte) {
	rt := r.rt
	fail := func(err error) { r.deliver(ch, result{err: r.describe(err)}) }
	v, err := rt.ParseJSON(arg)
	if err == nil {
		v, err = rt.Call(hook, v)
	}
	if err != nil {
		fail(err)
		return
	}
	ok := rt.Function("ok", 1, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		v := moejs.Arg(args, 0)
		if v.IsUndefined() {
			r.deliver(ch, result{data: []byte("null")})
			return moejs.Undefined(), nil
		}
		out, err := rt.AppendJSON(nil, v)
		if err != nil {
			fail(err)
		} else {
			r.deliver(ch, result{data: out})
		}
		return moejs.Undefined(), nil
	})
	rejected := rt.Function("fail", 1, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		r.deliver(ch, result{err: r.thrown(moejs.Arg(args, 0))})
		return moejs.Undefined(), nil
	})
	if _, err := rt.CallFunction(r.settle, moejs.Undefined(), v, ok, rejected); err != nil {
		fail(err)
	}
}

// describe turns an error of the runtime into one for the app: a throw
// into an *Error.
func (r *runtime) describe(err error) error {
	var exc *moejs.Exception
	if errors.As(err, &exc) {
		e := r.thrown(exc.Value)
		if s := r.rt.StackTrace(exc); s != "" {
			e.Stack = s
		}
		return e
	}
	return fmt.Errorf("plugin %s: %w", r.p.Manifest.ID, r.stopped(err))
}

// thrown reads what a plugin threw: an Error's message, code and stack,
// or any other value as text.
func (r *runtime) thrown(v moejs.Value) *Error {
	e := &Error{Plugin: r.p.Manifest.ID}
	if v.IsObject() {
		e.Message = r.prop(v, "message")
		e.Code = r.prop(v, "code")
		e.Stack = r.prop(v, "stack")
	}
	if e.Message == "" {
		e.Message = r.text(v)
	}
	return e
}

// prop is v's property key when it is a string.
func (r *runtime) prop(v moejs.Value, key string) string {
	p, err := r.rt.Get(v, key)
	if err != nil || !p.IsString() {
		return ""
	}
	return p.AsString().GoString()
}

// text is how console writes v: a string as it is, a plain value as JSON,
// anything else as JavaScript's String does.
func (r *runtime) text(v moejs.Value) string {
	if v.IsString() {
		return v.AsString().GoString()
	}
	if v.IsObject() && !v.AsObject().IsCallable() {
		if out, err := r.rt.AppendJSON(nil, v); err == nil && string(out) != "{}" {
			return string(out)
		}
	}
	if s, err := r.rt.Realm().ToString(v); err == nil {
		return s.GoString()
	}
	return v.String()
}
