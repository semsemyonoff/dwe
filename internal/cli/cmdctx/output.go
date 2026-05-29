package cmdctx

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// errorEnvelope is the JSON error shape written to stderr.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// CodedError is a typed user-facing error that carries a machine-readable
// code, an optional hint, and optional structured detail fields.
type CodedError struct {
	Code    string
	Message string
	Hint    string
	Details map[string]any
	Wrapped error
}

func (e *CodedError) Error() string { return e.Message }
func (e *CodedError) Unwrap() error { return e.Wrapped }

// WithHint attaches a human-readable suggestion and returns e for chaining.
func (e *CodedError) WithHint(hint string) *CodedError {
	e.Hint = hint
	return e
}

// WithDetail attaches a key/value detail field and returns e for chaining.
func (e *CodedError) WithDetail(key string, value any) *CodedError {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}

// Err creates a CodedError with the given code and message.
func Err(code, message string) *CodedError {
	return &CodedError{Code: code, Message: message}
}

// ErrWrap wraps an existing error under the given code, using the original
// error message as the message.
func ErrWrap(code string, err error) *CodedError {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return &CodedError{Code: code, Message: msg, Wrapped: err}
}

// WriteData dispatches between JSON and text output modes.
// In JSON mode it encodes data to cmd.OutOrStdout(); in text mode it
// calls renderText and writes the resulting string with a trailing newline.
func WriteData[T any](flags *RootFlags, cmd *cobra.Command, data T, renderText func(T) string) error {
	if flags.Output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		if flags.Pretty {
			enc.SetIndent("", "  ")
		}
		return enc.Encode(data)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), renderText(data))
	return err
}

// WriteError writes a JSON error envelope to stderr when in JSON mode.
// In text mode it is a no-op (fang's default error handler takes care of it).
func WriteError(flags *RootFlags, cmd *cobra.Command, err error) {
	if flags.Output != "json" || err == nil {
		return
	}
	env := buildErrorEnvelope(err)
	enc := json.NewEncoder(cmd.ErrOrStderr())
	if flags.Pretty {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(env)
}

// buildErrorEnvelope constructs the error envelope from err. If err is (or
// wraps) a *CodedError the structured fields are used; otherwise a generic
// internal_error envelope is returned.
func buildErrorEnvelope(err error) errorEnvelope {
	if ce, ok := errors.AsType[*CodedError](err); ok {
		return errorEnvelope{
			Error: errorBody{
				Code:    ce.Code,
				Message: ce.Message,
				Hint:    ce.Hint,
				Details: ce.Details,
			},
		}
	}
	return errorEnvelope{
		Error: errorBody{
			Code:    "internal_error",
			Message: err.Error(),
		},
	}
}
