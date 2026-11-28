package app

import (
	"context"
	"errors"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/nsgo-dev/nsgo-media/app/internal/plugin"
	"github.com/nsgo-dev/nsgo-media/app/locales"
)

// fromGitHubState is how finding a repository's plugin goes: the
// repository typed, finding it, or why it failed.
type fromGitHubState struct {
	// n counts the searches, so one the viewer left is dropped.
	n    int
	busy bool
	err  error
}

// openFromGitHub asks for a repository to install the plugin of.
func (sh *shell) openFromGitHub() {
	sh.fromGitHub.Set(fromGitHubState{n: sh.fromGitHub.GetUntracked().n + 1})
	sh.typingRepo.Set(true)
}

// fromGitHubDialog asks for a repository, finds the plugin zip of its
// latest release and reads it, then hands it to the install dialog to
// approve its hosts.
func (sh *shell) fromGitHubDialog(cx *core.Scope) core.Node {
	text := core.NewSignal(cx, "")
	close := func() {
		sh.typingRepo.Set(false)
		text.Set("")
	}
	find := func() {
		st := sh.fromGitHub.GetUntracked()
		if st.busy {
			return
		}
		repo, ok := parseRepo(text.GetUntracked())
		if !ok {
			sh.fromGitHub.Set(fromGitHubState{n: st.n, err: errNotARepo})
			return
		}
		n := st.n + 1
		sh.fromGitHub.Set(fromGitHubState{n: n, busy: true})
		taken := sh.takenIDs()
		call(cx, sh.ctx, func(ctx context.Context) (*candidate, error) {
			r, err := sh.gitHub.latest(ctx, repo)
			if err != nil {
				return nil, err
			}
			return sh.library.inspectRelease(ctx, sh.gitHub, r, taken)
		}, func(c *candidate, err error) {
			if sh.fromGitHub.GetUntracked().n != n || !sh.typingRepo.GetUntracked() {
				return
			}
			if err != nil {
				sh.fromGitHub.Set(fromGitHubState{n: n, err: err})
				return
			}
			cx.Batch(func() {
				close()
				sh.install.Set(installState{})
				sh.installing.Set(c)
			})
		})
	}
	return ui.Dialog{
		TestID: "from-github",
		Bare:   true,
		Open:   sh.typingRepo.Get,
		OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				close()
			}
		},
		NoCloseOnBackdropPress: true,
		Label:                  locales.InstallFromGitHub(cx),
		Style:                  core.Style{Width: core.Pt(480), CornerRadius: 16},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			if !sh.typingRepo.Get() {
				return nil
			}
			busy := func() bool { return sh.fromGitHub.Get().busy }
			return core.View{Children: []core.Node{
				core.View{Class: "gap-4 px-6 pt-6 pb-5", Children: []core.Node{
					core.View{Class: "flex-row items-center gap-3", Children: []core.Node{
						core.View{Class: "size-10 items-center justify-center rounded-xl bg-foreground", Children: []core.Node{
							core.Icon{SVG: lucide.GitFork(), Class: "size-5 text-background"},
						}},
						core.Text{Content: locales.InstallFromGitHub(cx), Class: "text-[17px] font-bold text-foreground"},
					}},
					core.Text{Content: locales.InstallFromGitHubHint(cx), Class: "text-[13px] leading-relaxed text-muted-foreground"},
					core.View{Class: "gap-2", Children: []core.Node{
						ui.Input{
							TestID:      "repository",
							Label:       locales.Repository(cx),
							Placeholder: locales.RepositoryPlaceholder(cx),
							Value:       text.Get,
							OnChange: func(s string) {
								text.Set(s)
								if st := sh.fromGitHub.GetUntracked(); st.err != nil {
									sh.fromGitHub.Set(fromGitHubState{n: st.n})
								}
							},
							OnSubmit:       func(string) { find() },
							AutoFocus:      true,
							NoAutoCorrect:  true,
							AutoCapitalize: core.AutoCapitalizeNone,
							Disabled:       busy,
							Invalid:        func() bool { return sh.fromGitHub.Get().err != nil },
							StyleFunc: func() core.Style {
								if err := sh.fromGitHub.Get().err; err != nil {
									c := "error-icon"
									if errors.As(err, new(*plugin.TooNewError)) {
										c = "warn-icon"
									}
									return core.Style{BorderWidth: 2, BorderColor: paletteColor(cx, c)}
								}
								return core.Style{}
							},
							Style: core.Style{Height: core.Pt(40), CornerRadius: 12, FontSize: 14, Width: core.Pct(100)},
						},
						core.Dynamic(func(cx *core.Scope) core.Node {
							st := sh.fromGitHub.Get()
							switch {
							case st.busy:
								return core.View{Class: "flex-row items-center gap-1.5", Children: []core.Node{
									ui.Spinner{Size: 14},
									core.Text{TestID: "finding-release", Content: locales.FindingRelease(cx), Class: "text-xs text-muted-foreground"},
								}}
							case st.err != nil:
								icon, ink := lucide.CircleAlert, "text-error-ink"
								if errors.As(st.err, new(*plugin.TooNewError)) {
									icon, ink = lucide.TriangleAlert, "text-warn-ink"
								}
								return core.View{Class: "flex-row items-start gap-1.5", Children: []core.Node{
									core.Icon{SVG: icon(), Class: "mt-px size-3.5 " + ink},
									core.Text{TestID: "from-github-error", Content: installError(cx, st.err), Class: "flex-1 text-xs " + ink, Style: core.Style{FlexShrink: 1}},
								}}
							}
							return nil
						}),
					}},
				}},
				dialogActions(nil,
					ui.Button{TestID: "cancel-from-github", Text: locales.Cancel(cx), Variant: ui.ButtonOutline, OnPress: close},
					ui.Button{TestID: "find-release", Text: locales.Continue(cx), Busy: busy,
						Disabled: func() bool { return strings.TrimSpace(text.Get()) == "" },
						OnPress:  find},
				),
			}}
		})},
	}
}

// errNotARepo is text that names no repository.
var errNotARepo = errors.New("not a repository")

// takenIDs reports the ids of the plugins loaded.
func (sh *shell) takenIDs() func(string) bool {
	taken := map[string]bool{}
	for _, p := range sh.plugins.GetUntracked() {
		taken[p.Manifest.ID] = true
	}
	return func(id string) bool { return taken[id] }
}

// gitHubMessage is why asking GitHub failed, if it did.
func gitHubMessage(cx *core.Scope, err error) (string, bool) {
	if errors.Is(err, errNotARepo) {
		return locales.NotARepository(cx), true
	}
	var g *gitHubError
	if !errors.As(err, &g) {
		return "", false
	}
	switch g.problem {
	case problemNoRepo:
		return locales.NoRepository(cx, g.repo), true
	case problemNoRelease:
		return locales.NoRelease(cx), true
	case problemNoZip:
		return locales.NoPluginZip(cx), true
	case problemLimited:
		return locales.GitHubLimited(cx), true
	}
	return locales.GitHubUnreachable(cx), true
}

// paletteColor is the theme's color of name, as classes name it.
func paletteColor(cx *core.Scope, name string) core.Color {
	p := cx.Palette()
	c, _ := p.Color(name)
	return c
}
