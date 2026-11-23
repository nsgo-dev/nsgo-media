package app

import "github.com/nsgo-dev/nsgo/core"

// theme is the app's look: NSGo's theme with the app's coral accent and
// the few roles its pages add, which classes name (bg-skeleton,
// text-accent-ink).
func theme() *core.Theme {
	t := core.DefaultTheme()
	t.Light.Accent, t.Light.AccentHover, t.Light.AccentPressed = core.RGB(0xe8553a), core.RGB(0xd94a30), core.RGB(0xc8442b)
	t.Light.Focus, t.Light.Selection = core.RGBA(0xe8553a, 0.5), core.RGBA(0xe8553a, 0.2)
	t.Dark.Accent, t.Dark.AccentHover, t.Dark.AccentPressed = core.RGB(0xff6b4a), core.RGB(0xff7f62), core.RGB(0xe8553a)
	t.Dark.Focus, t.Dark.Selection = core.RGBA(0xff6b4a, 0.7), core.RGBA(0xff6b4a, 0.24)
	for name, c := range map[string]core.Color{
		"accentFill": core.RGB(0xe8553a),
		// accent-ink is the accent as text on a light fill of it.
		"accent-ink": core.RGB(0xc8442b),
		"skeleton":   core.RGBA(0xe4e4e7, 0.7),
		"chip":       core.RGB(0xf4f4f5),
		"well":       core.RGB(0xe4e4e7),
		"faint-ink":  core.RGB(0xa1a1aa),
		"warn-soft":  core.RGB(0xfffbeb), "warn-line": core.RGB(0xfde68a), "warn-ink": core.RGB(0x92400e), "warn-icon": core.RGB(0xd97706),
		"error-soft": core.RGB(0xfff1f2), "error-line": core.RGB(0xfecdd3), "error-ink": core.RGB(0xbe123c), "error-icon": core.RGB(0xf43f5e),
		"ok-icon": core.RGB(0x16a88e),
	} {
		t.Light.SetColor(name, c)
	}
	for name, c := range map[string]core.Color{
		"accentFill": core.RGB(0xff6b4a),
		"accent-ink": core.RGB(0xff8a6b),
		"skeleton":   core.RGBA(0xffffff, 0.08),
		"chip":       core.RGBA(0xffffff, 0.08),
		"well":       core.RGBA(0xffffff, 0.06),
		"faint-ink":  core.RGB(0x71717a),
		"warn-soft":  core.RGBA(0xf59e0b, 0.12), "warn-line": core.RGBA(0xf59e0b, 0.3), "warn-ink": core.RGB(0xfcd34d), "warn-icon": core.RGB(0xfbbf24),
		"error-soft": core.RGBA(0xf43f5e, 0.12), "error-line": core.RGBA(0xf43f5e, 0.3), "error-ink": core.RGB(0xfda4af), "error-icon": core.RGB(0xfb7185),
		"ok-icon": core.RGB(0x3bd6b8),
	} {
		t.Dark.SetColor(name, c)
	}
	return t
}
