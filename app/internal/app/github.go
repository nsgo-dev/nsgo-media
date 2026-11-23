package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/oneo-me/nsgo-media/app/internal/store"
)

// gitHubAPI is GitHub's REST API, which the app asks for plugins'
// releases.
const gitHubAPI = "https://api.github.com"

// gitHub finds the plugin zips of repositories' latest releases. Its
// methods may be called from any goroutine.
type gitHub struct {
	// api is the REST API's root.
	api    string
	client *http.Client
}

// release is a repository's latest release, never a draft or a
// pre-release, and the zips it holds.
type release struct {
	store.Release
	// notes are its description, as its author wrote them.
	notes string
	zips  []asset
}

type asset struct {
	name, url string
	size      int64
}

// gitHubProblem is what went wrong asking GitHub.
type gitHubProblem int

const (
	// problemNoRepo is no repository by that name, or a private one.
	problemNoRepo gitHubProblem = iota
	problemNoRelease
	// problemNoZip is a latest release without a zip holding a plugin.
	problemNoZip
	// problemLimited is GitHub limiting the requests of this address.
	problemLimited
	// problemUnreachable is GitHub not answering, or answering an error.
	problemUnreachable
)

// gitHubError is why a repository's plugin cannot be found.
type gitHubError struct {
	problem gitHubProblem
	repo    string
	// err is the request's own error, for problemUnreachable.
	err error
}

func (e *gitHubError) Error() string {
	switch e.problem {
	case problemNoRepo:
		return "there is no repository " + e.repo + " on GitHub"
	case problemNoRelease:
		return e.repo + " has no release"
	case problemNoZip:
		return "the latest release of " + e.repo + " has no plugin zip"
	case problemLimited:
		return "GitHub limits requests now"
	}
	return "GitHub could not be reached: " + e.err.Error()
}

func (e *gitHubError) Unwrap() error { return e.err }

// repoPart is a GitHub owner's or repository's name.
var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// parseRepo reads a repository typed as owner/repo or its URL, such as
// https://github.com/owner/repo or one of its pages, as owner/repo.
func parseRepo(s string) (string, bool) {
	s = strings.TrimSpace(s)
	rest, isURL := s, false
	for _, prefix := range []string{"https://", "http://"} {
		if r, ok := strings.CutPrefix(rest, prefix); ok {
			rest, isURL = r, true
		}
	}
	rest = strings.TrimPrefix(rest, "www.")
	if r, ok := strings.CutPrefix(rest, "github.com/"); ok {
		rest, isURL = r, true
	} else if isURL {
		return "", false
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || (!isURL && len(parts) > 2) {
		return "", false
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	for _, p := range []string{owner, repo} {
		if !repoPart.MatchString(p) || p == "." || p == ".." {
			return "", false
		}
	}
	return owner + "/" + repo, true
}

// latest asks for the latest release of repo, as owner/repo.
func (g *gitHub) latest(ctx context.Context, repo string) (*release, error) {
	var answer struct {
		Tag        string    `json:"tag_name"`
		Body       string    `json:"body"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
		Published  time.Time `json:"published_at"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	status, err := g.get(ctx, repo, "/repos/"+repo+"/releases/latest", &answer)
	if status == http.StatusNotFound {
		// GitHub answers the same for a repository without a release
		// and for no repository.
		status, err = g.get(ctx, repo, "/repos/"+repo, nil)
		if status == http.StatusNotFound {
			return nil, &gitHubError{problem: problemNoRepo, repo: repo}
		}
		if err == nil {
			err = &gitHubError{problem: problemNoRelease, repo: repo}
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	// The latest release is never one of these; a server that answers
	// one is taken at its word.
	if answer.Draft || answer.Prerelease {
		return nil, &gitHubError{problem: problemNoRelease, repo: repo}
	}
	r := &release{Release: store.Release{Repo: repo, Tag: answer.Tag, Published: answer.Published}, notes: strings.TrimSpace(answer.Body)}
	for _, a := range answer.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
			r.zips = append(r.zips, asset{name: a.Name, url: a.URL, size: a.Size})
		}
	}
	if len(r.zips) == 0 {
		return nil, &gitHubError{problem: problemNoZip, repo: repo}
	}
	return r, nil
}

// get asks the API for path and decodes its JSON into v, unless nil. It
// answers the status, and an error for any but 200.
func (g *gitHub) get(ctx context.Context, repo, path string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := g.do(req)
	if err != nil {
		return 0, &gitHubError{problem: problemUnreachable, repo: repo, err: err}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return resp.StatusCode, &gitHubError{problem: problemLimited, repo: repo}
	case resp.StatusCode != http.StatusOK:
		return resp.StatusCode, &gitHubError{problem: problemUnreachable, repo: repo, err: errors.New(resp.Status)}
	}
	if v == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v); err != nil {
		return resp.StatusCode, &gitHubError{problem: problemUnreachable, repo: repo, err: err}
	}
	return resp.StatusCode, nil
}

func (g *gitHub) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "NSGo-Player/"+Version)
	return g.client.Do(req)
}

// download reads the first zip of r that holds a plugin, at its root or
// in its only folder.
func (g *gitHub) download(ctx context.Context, r *release) (fs.FS, error) {
	var errs []error
	for _, a := range r.zips {
		fsys, err := g.zip(ctx, a)
		if err == nil {
			if root, err := pluginRoot(fsys); err == nil {
				return root, nil
			}
			continue
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return nil, &gitHubError{problem: problemNoZip, repo: r.Repo}
}

// zip downloads a, bounded by the size of a plugin.
func (g *gitHub) zip(ctx context.Context, a asset) (fs.FS, error) {
	tooLarge := fmt.Errorf("%s is larger than %d MB", a.name, store.MaxPluginSize>>20)
	if a.size > store.MaxPluginSize {
		return nil, tooLarge
	}
	u, err := url.Parse(a.url)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%s has no download URL", a.name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := g.do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", a.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", a.name, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, store.MaxPluginSize+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", a.name, err)
	}
	if len(data) > store.MaxPluginSize {
		return nil, tooLarge
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s is not a zip: %w", a.name, err)
	}
	return zr, nil
}
