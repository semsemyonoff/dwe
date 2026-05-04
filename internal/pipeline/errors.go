package pipeline

import "errors"

// ErrSilent is returned by Run when a step has already printed its own error
// message and the caller should not print an additional message.
var ErrSilent = errors.New("")
