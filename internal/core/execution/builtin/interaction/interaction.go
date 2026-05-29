// Package interaction groups the user-prompt builtins: confirm and message.
package interaction

import "devbox-cli/internal/core/execution/builtin/spec"

// Builtins returns the interaction builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"confirm": {Impl: Confirm{}, Kind: spec.KindAction},
		"message": {Impl: Message{}, Kind: spec.KindAction},
	}
}
