package app

import (
	"context"

	"github.com/nsgo-dev/nsgo/core"
)

// call runs fn off the engine goroutine, as plugin calls block, and hands
// its result to done on it, unless cx was disposed first. ctx is the
// context of cx's component, taken as it renders: one taken in a handler
// ends with the handler.
func call[T any](cx *core.Scope, ctx context.Context, fn func(context.Context) (T, error), done func(T, error)) {
	go func() {
		v, err := fn(ctx)
		cx.Post(func() { done(v, err) })
	}()
}
