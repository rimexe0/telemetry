package desktop

import "errors"

// ErrGrayscaleUnknown means this desktop cannot tell whether the grayscale
// filter is on. Callers report the state as unknown and do nothing on a
// guess.
var ErrGrayscaleUnknown = errors.New("the grayscale filter's state cannot be read on this desktop")
