package stats

import (
	"strings"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
)

func TestAgent(t *testing.T) {
	if a := agent(core.OSWindows); !strings.HasPrefix(a, "Mozilla/5.0 (Windows NT") {
		t.Errorf("windows agent %q", a)
	}
	if a := agent(core.OSWeb); a != "" {
		t.Errorf("web agent %q, want none", a)
	}
}
