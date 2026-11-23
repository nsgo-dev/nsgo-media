package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Manifest is a plugin's plugin.yaml: everything the app shows or asks the
// viewer before it runs any of the plugin's code.
type Manifest struct {
	ID            string      `yaml:"id"`
	Name          string      `yaml:"name"`
	Version       string      `yaml:"version"`
	MinAppVersion string      `yaml:"minAppVersion"`
	Main          string      `yaml:"main"`
	Media         string      `yaml:"media"`
	Permissions   Permissions `yaml:"permissions"`
	Settings      []Setting   `yaml:"settings,omitempty"`
	Navigation    []Entry     `yaml:"navigation,omitempty"`
}

// Permissions are what a plugin may reach beyond its sandbox.
type Permissions struct {
	// Network lists the hosts fetch reaches; "*.example.com" matches the
	// subdomains of example.com, not example.com itself.
	Network []string `yaml:"network,omitempty"`
}

// The kinds of media a plugin serves.
const (
	MediaVideo = "video"
	MediaAudio = "audio"
)

// Setting is a value the viewer sets for a plugin, which it reads with
// host.settings.get.
type Setting struct {
	Key   string      `yaml:"key"`
	Label string      `yaml:"label"`
	Type  SettingType `yaml:"type"`
	// Default is a string for text, secret and choice settings and a bool
	// for toggles; nil is "" or false.
	Default any      `yaml:"default,omitempty"`
	Choices []Choice `yaml:"choices,omitempty"`
}

// SettingType is how a setting is edited.
type SettingType string

// The setting types.
const (
	SettingText   SettingType = "text"
	SettingSecret SettingType = "secret"
	SettingToggle SettingType = "toggle"
	SettingChoice SettingType = "choice"
)

// Choice is one value of a choice setting.
type Choice struct {
	Value string `yaml:"value"`
	Label string `yaml:"label"`
}

// Entry is a sidebar entry under the plugin, which opens browse with its id.
type Entry struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
}

// TooNewError refuses a plugin that needs a newer app.
type TooNewError struct {
	ID, MinAppVersion, AppVersion string
}

func (e *TooNewError) Error() string {
	return fmt.Sprintf("plugin %s needs NSGo Player %s or later; this is %s, update the app", e.ID, e.MinAppVersion, e.AppVersion)
}

// ManifestFile is the manifest's name in a plugin's folder.
const ManifestFile = "plugin.yaml"

var idPattern = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

// ParseManifest reads and validates plugin.yaml for an app of appVersion.
// A plugin needing a newer app is a *TooNewError, checked before the rest
// so that a newer manifest's fields never hide it.
func ParseManifest(data []byte, appVersion string) (*Manifest, error) {
	var head struct {
		ID            string `yaml:"id"`
		MinAppVersion string `yaml:"minAppVersion"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if v, ok := parseVersion(head.MinAppVersion); ok {
		if app, ok := parseVersion(appVersion); ok && compareVersions(v, app) > 0 {
			return nil, &TooNewError{ID: head.ID, MinAppVersion: head.MinAppVersion, AppVersion: appVersion}
		}
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if !idPattern.MatchString(m.ID) {
		fail("id %q is not lowercase letters and digits joined by dots or dashes", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		fail("name is empty")
	}
	if _, ok := parseVersion(m.Version); !ok {
		fail("version %q is not a version such as 1.0.0", m.Version)
	}
	if _, ok := parseVersion(m.MinAppVersion); !ok {
		fail("minAppVersion %q is not a version such as 1.0.0", m.MinAppVersion)
	}
	if ext := path.Ext(m.Main); m.Main != "" && (!fsPath(m.Main) || (ext != ".js" && ext != ".ts")) {
		fail("main %q is not a .js or .ts file in the plugin's folder", m.Main)
	}
	if m.Media != MediaVideo && m.Media != MediaAudio {
		fail("media %q is neither %q nor %q", m.Media, MediaVideo, MediaAudio)
	}
	for _, h := range m.Permissions.Network {
		if !validHost(h) {
			fail("network host %q is not a host name such as api.example.com or *.example.com", h)
		}
	}
	keys := map[string]bool{}
	for _, s := range m.Settings {
		if s.Key == "" || keys[s.Key] {
			fail("setting key %q is empty or repeated", s.Key)
		}
		keys[s.Key] = true
		if strings.TrimSpace(s.Label) == "" {
			fail("setting %q has no label", s.Key)
		}
		if err := s.check(s.Default, true); err != nil {
			fail("setting %q: default %w", s.Key, err)
		}
		if (s.Type == SettingChoice) != (len(s.Choices) > 0) {
			fail("setting %q: only a choice setting has choices, and it needs some", s.Key)
		}
	}
	ids := map[string]bool{}
	for _, e := range m.Navigation {
		if e.ID == "" || ids[e.ID] {
			fail("navigation id %q is empty or repeated", e.ID)
		}
		ids[e.ID] = true
		if strings.TrimSpace(e.Label) == "" {
			fail("navigation entry %q has no label", e.ID)
		}
	}
	return errors.Join(errs...)
}

// Setting returns the manifest's setting of key.
func (m *Manifest) Setting(key string) (Setting, bool) {
	for _, s := range m.Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// check reports whether v fits the setting; nil fits when nilOK.
func (s Setting) check(v any, nilOK bool) error {
	if v == nil {
		if nilOK {
			return nil
		}
		return errors.New("is missing")
	}
	switch s.Type {
	case SettingText, SettingSecret:
		if _, ok := v.(string); !ok {
			return errors.New("is not a string")
		}
	case SettingToggle:
		if _, ok := v.(bool); !ok {
			return errors.New("is not a boolean")
		}
	case SettingChoice:
		c, _ := v.(string)
		for _, ch := range s.Choices {
			if ch.Value == c {
				return nil
			}
		}
		return fmt.Errorf("%v is none of the choices", v)
	default:
		return fmt.Errorf("type %q is none of text, secret, toggle and choice", s.Type)
	}
	return nil
}

// value is the setting's value of v, its default when v is nil.
func (s Setting) value(v any) any {
	if v == nil {
		v = s.Default
	}
	if v == nil {
		if s.Type == SettingToggle {
			return false
		}
		return ""
	}
	return v
}

// fsPath reports whether p is a clean relative path inside a folder.
func fsPath(p string) bool {
	return p != "" && p == path.Clean(p) && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../")
}

func validHost(h string) bool {
	h = strings.TrimPrefix(h, "*.")
	if h == "" || strings.ContainsAny(h, "*:/ ") || h != strings.ToLower(h) {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
	}
	return true
}

// parseVersion reads major.minor.patch, ignoring a pre-release or build
// suffix.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Newer reports whether version a is newer than b, both such as 1.0.0;
// one that is not a version is never newer.
func Newer(a, b string) bool {
	va, ok := parseVersion(a)
	vb, okb := parseVersion(b)
	return ok && okb && compareVersions(va, vb) > 0
}
