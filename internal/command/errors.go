package command

import pipeline "devbox-cli/internal/pipeline"

// ErrSilent is returned when a command has already printed its own error
// message and the root command should exit with code 1 without reprinting.
// It is the same sentinel defined in internal/pipeline.
var ErrSilent = pipeline.ErrSilent
