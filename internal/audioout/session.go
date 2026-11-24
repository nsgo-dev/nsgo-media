package audioout

// Event is what the system did to the audio output under the app, which
// only iOS reports.
type Event struct {
	Kind EventKind
	// Resume, as an interruption ends, says the system lets the app play
	// again (AVAudioSessionInterruptionOptionShouldResume).
	Resume bool
}

// EventKind tells an Event's kind.
type EventKind int

const (
	// Interrupted is an interruption beginning, such as a call or Siri:
	// the system has stopped the output.
	Interrupted EventKind = iota + 1
	// InterruptionEnded is the interruption ending.
	InterruptionEnded
	// RouteLost is the output's device going away, such as headphones
	// unplugged; the sound would go on through the speaker.
	RouteLost
)

// Watch calls fn with each Event the system posts until stop, on a thread
// of the system's, so fn must return quickly. Systems that post none never
// call it.
func Watch(fn func(Event)) (stop func()) { return watch(fn) }

// BackgroundAudio reports the app declaring iOS's audio background mode
// (UIBackgroundModes' audio), under which the system lets it play on in
// the background. Other systems report false.
func BackgroundAudio() bool { return backgroundAudio() }
