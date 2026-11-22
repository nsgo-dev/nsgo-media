package audioout

import "errors"

// iOS has no API to send an IEC 61937 stream to a receiver, so the caller
// plays PCM.
func openIEC(IEC, FillIEC) (Output, error) {
	return nil, errors.New("audioout: iOS passes no audio through")
}
