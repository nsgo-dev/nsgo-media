package player

import (
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

// category is the audio session's category, by its string.
func category() string {
	purego.Dlopen("/System/Library/Frameworks/AVFAudio.framework/AVFAudio", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	session := objc.ID(objc.GetClass("AVAudioSession")).Send(objc.RegisterName("sharedInstance"))
	name := session.Send(objc.RegisterName("category"))
	if name == 0 {
		return ""
	}
	return objc.Send[string](name, objc.RegisterName("UTF8String"))
}

// A playing player sets the session to Playback, which the Ring/Silent
// switch does not silence, and gives back the category from before.
func TestPlayerPlaysUnderPlayback(t *testing.T) {
	before := category()
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(fixture(t, "av.mkv"))
		return View{Player: p}
	})
	p.Play()
	play(t, app, 3*time.Second, func() bool { return p.State() == Playing })
	if got := category(); got != "AVAudioSessionCategoryPlayback" {
		t.Fatalf("category %q while playing", got)
	}
	p.Pause()
	// The session goes back a moment after its last holder lets go.
	deadline := time.Now().Add(3 * time.Second)
	for category() != before {
		if time.Now().After(deadline) {
			t.Fatalf("category %q after the pause, want %q", category(), before)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p.backgroundAudio {
		t.Error("the test app reads an audio background mode it does not declare")
	}
}
