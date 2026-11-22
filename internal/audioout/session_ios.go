package audioout

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// On iOS the system tells interruptions and route changes through
// AVAudioSession's notifications, which Watch observes. NSGo holds the
// session itself (its category and activation); observing its
// notifications changes nothing of it.

const (
	interruptionBegan    = 1 // AVAudioSessionInterruptionTypeBegan
	optionShouldResume   = 1 // AVAudioSessionInterruptionOptionShouldResume
	oldDeviceUnavailable = 2 // AVAudioSessionRouteChangeReasonOldDeviceUnavailable
)

var (
	avOnce  sync.Once
	avNames map[string]objc.ID
)

// avName is the NSString of one of AVFAudio's constants, 0 when it does
// not load.
func avName(name string) objc.ID {
	avOnce.Do(func() {
		avNames = map[string]objc.ID{}
		lib, err := purego.Dlopen("/System/Library/Frameworks/AVFAudio.framework/AVFAudio", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		for _, n := range []string{
			"AVAudioSessionInterruptionNotification", "AVAudioSessionInterruptionTypeKey", "AVAudioSessionInterruptionOptionKey",
			"AVAudioSessionRouteChangeNotification", "AVAudioSessionRouteChangeReasonKey",
		} {
			if sym, err := purego.Dlsym(lib, n); err == nil && sym != 0 {
				avNames[n] = *(*objc.ID)(pointer(sym))
			}
		}
	})
	return avNames[name]
}

func watch(fn func(Event)) func() {
	center := objc.ID(objc.GetClass("NSNotificationCenter")).Send(objc.RegisterName("defaultCenter"))
	add := objc.RegisterName("addObserverForName:object:queue:usingBlock:")
	var observers []objc.ID
	observe := func(name string, handle func(info objc.ID)) {
		n := avName(name)
		if n == 0 {
			return
		}
		block := objc.NewBlock(func(_ objc.Block, note objc.ID) {
			handle(note.Send(objc.RegisterName("userInfo")))
		})
		// No object and no queue: the session's notifications, on the
		// thread posting them.
		obs := center.Send(add, n, objc.ID(0), objc.ID(0), block)
		block.Release()
		if obs != 0 {
			observers = append(observers, obs.Send(objc.RegisterName("retain")))
		}
	}
	observe("AVAudioSessionInterruptionNotification", func(info objc.ID) {
		if uintValue(info, "AVAudioSessionInterruptionTypeKey") == interruptionBegan {
			fn(Event{Kind: Interrupted})
			return
		}
		fn(Event{Kind: InterruptionEnded, Resume: uintValue(info, "AVAudioSessionInterruptionOptionKey")&optionShouldResume != 0})
	})
	observe("AVAudioSessionRouteChangeNotification", func(info objc.ID) {
		if uintValue(info, "AVAudioSessionRouteChangeReasonKey") == oldDeviceUnavailable {
			fn(Event{Kind: RouteLost})
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, obs := range observers {
				center.Send(objc.RegisterName("removeObserver:"), obs)
				obs.Send(objc.RegisterName("release"))
			}
		})
	}
}

// uintValue is the NSNumber under key in a notification's userInfo, 0
// when it has none.
func uintValue(info objc.ID, key string) uint {
	if info == 0 {
		return 0
	}
	v := info.Send(objc.RegisterName("objectForKey:"), avName(key))
	if v == 0 {
		return 0
	}
	return objc.Send[uint](v, objc.RegisterName("unsignedIntegerValue"))
}
