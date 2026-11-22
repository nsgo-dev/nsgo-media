//go:build !ios

package audioout

func watch(func(Event)) func() { return func() {} }
