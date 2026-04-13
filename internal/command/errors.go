package command

import "errors"

// ErrSilent is returned when a command has already printed its own error
// message and the root command should exit with code 1 without reprinting.
var ErrSilent = errors.New("")
